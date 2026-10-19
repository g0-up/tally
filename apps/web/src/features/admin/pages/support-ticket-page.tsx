import { useState } from "react";
import { useParams } from "react-router";
import { useAdminSession } from "../../../shared/api/platform-queries";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Grid, Stack } from "../../../shared/ui/grid";
import { Textarea } from "../../../shared/ui/input";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { TextLink } from "../../../shared/ui/link-button";
import { LIST_LOADING_TEXT, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import { useReplyTicket, useUpdateTicket } from "../api/mutations";
import { useAdmins, useTicket } from "../api/queries";
import type { TicketPriority, TicketStatus, TicketView } from "../api/schemas";
import { Assignee } from "../components/assignee";
import { LoadError } from "../components/load-error";
import { TICKET_PRIORITY, TICKET_STATUS, entriesOf } from "../lib/labels";

const MAX_REPLY = 10_000;

/** Yêu cầu hỗ trợ (/admin/support/tickets/:id): the conversation, a reply, status, priority and assignment. */
export default function SupportTicketPage() {
  const { id = "" } = useParams();
  const ticket = useTicket(id);

  if (ticket.isPending) return <ListLoading />;
  if (ticket.isError) {
    return (
      <>
        <PageHeader eyebrow="Hỗ trợ" title="Yêu cầu hỗ trợ" />
        <LoadError what="yêu cầu hỗ trợ" error={ticket.error} onRetry={() => void ticket.refetch()} />
      </>
    );
  }
  const t = ticket.data;
  return (
    <>
      <PageHeader eyebrow={`Hỗ trợ · ${t.number}`} title={t.subject} actions={<TextLink to="/admin/support">Về danh sách</TextLink>} />
      <Grid cols="g-main">
        <Stack>
          <Card title="Trao đổi">
            <Timeline
              items={t.messages.map((m) => ({
                key: m.id,
                time: fdt(m.created_at),
                content: (
                  <>
                    <b>{m.author_type === "owner" ? "Chủ cửa hàng" : "Hỗ trợ Tally"}</b>
                    {m.attachment_ids.length > 0 ? ` · ${m.attachment_ids.length} tệp đính kèm` : null}
                    <br />
                    <span style={{ whiteSpace: "pre-wrap" }}>{m.body}</span>
                  </>
                ),
              }))}
              empty="Chưa có tin nhắn."
            />
          </Card>
          <ReplyCard ticket={t} />
        </Stack>
        <Stack>
          <Card title="Thông tin">
            <KeyValueGrid
              items={[
                { label: "Trạng thái", value: <StatusPill {...TICKET_STATUS[t.status]} /> },
                { label: "Ưu tiên", value: TICKET_PRIORITY[t.priority] },
                { label: "Hạn xử lý", value: t.sla_due_at ? fdt(t.sla_due_at) : "—" },
                { label: "Phân công", value: <Assignee adminId={t.assigned_admin_id} /> },
                { label: "Tạo lúc", value: fdt(t.created_at) },
                { label: "Cập nhật lúc", value: fdt(t.updated_at) },
              ]}
            />
          </Card>
          <StatusCard key={`${t.status}:${t.priority}`} ticket={t} />
          <AssignCard key={t.assigned_admin_id ?? ""} ticket={t} />
        </Stack>
      </Grid>
    </>
  );
}

function ReplyCard({ ticket }: { ticket: TicketView }) {
  const [body, setBody] = useState("");
  const [error, setError] = useState<string>();
  const reply = useReplyTicket(ticket.id, {
    success: () => (
      <>
        Đã gửi trả lời cho <b>{ticket.number}</b>.
      </>
    ),
    after: () => setBody(""),
  });
  const closed = ticket.status === "closed";
  const submit = () => {
    const text = body.trim();
    if (text === "") return setError("Nhập nội dung trả lời.");
    if (text.length > MAX_REPLY) return setError(`Nội dung tối đa ${MAX_REPLY} ký tự.`);
    void reply.run(text);
  };
  return (
    <Card title="Trả lời">
      {closed ? <p className="hint">Yêu cầu đã đóng; không gửi thêm tin nhắn được.</p> : null}
      <Field label="Nội dung trả lời" error={error}>
        <Textarea
          rows={4}
          value={body}
          disabled={closed}
          invalid={error !== undefined}
          onChange={(e) => {
            setBody(e.target.value);
            setError(undefined);
          }}
        />
      </Field>
      <ButtonRow>
        <Button variant="primary" disabled={closed} pending={reply.pending} pendingLabel="Đang gửi…" onClick={submit}>
          Gửi trả lời
        </Button>
      </ButtonRow>
    </Card>
  );
}

function StatusCard({ ticket }: { ticket: TicketView }) {
  const [status, setStatus] = useState<TicketStatus>(ticket.status);
  const [priority, setPriority] = useState<TicketPriority>(ticket.priority);
  const update = useUpdateTicket(ticket.id, {
    success: (t) => (
      <>
        Đã cập nhật <b>{t.number}</b>.
      </>
    ),
  });
  const changed = status !== ticket.status || priority !== ticket.priority;
  const save = () =>
    void update.run({
      ...(status !== ticket.status ? { status } : {}),
      ...(priority !== ticket.priority ? { priority } : {}),
    });
  return (
    <Card title="Xử lý">
      <FormGrid>
        <Field label="Trạng thái">
          <select className="in" value={status} onChange={(e) => setStatus(e.target.value as TicketStatus)}>
            {entriesOf(TICKET_STATUS).map(([v, s]) => (
              <option key={v} value={v}>
                {s.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Ưu tiên">
          <select className="in" value={priority} onChange={(e) => setPriority(e.target.value as TicketPriority)}>
            {entriesOf(TICKET_PRIORITY).map(([v, label]) => (
              <option key={v} value={v}>
                {label}
              </option>
            ))}
          </select>
        </Field>
      </FormGrid>
      <ButtonRow>
        <Button variant="primary" disabled={!changed} pending={update.pending} onClick={save}>
          Lưu thay đổi
        </Button>
      </ButtonRow>
    </Card>
  );
}

function AssignCard({ ticket }: { ticket: TicketView }) {
  const me = useAdminSession().data?.admin;
  const admins = useAdmins();
  const choices = (admins.data ?? []).filter((a) => a.active && a.id !== ticket.assigned_admin_id);
  const [adminId, setAdminId] = useState("");
  const [error, setError] = useState<string>();
  // Which of the three buttons sent the running change, so only that one shows the wait.
  const [action, setAction] = useState<"assign" | "self" | "unassign">("assign");
  const update = useUpdateTicket(ticket.id, {
    success: (t) => (t.assigned_admin_id ? <>Đã phân công {t.number}.</> : <>Đã bỏ phân công {t.number}.</>),
    setFieldError: (field, message) => {
      if (field === "assigned_admin_id") setError(message);
    },
  });
  const assign = () => {
    if (adminId === "") return setError("Chọn người nhận việc.");
    setAction("assign");
    void update.run({ assigned_admin_id: adminId });
  };
  return (
    <Card title="Phân công">
      {admins.isError ? <LoadError what="danh sách quản trị viên" error={admins.error} onRetry={() => void admins.refetch()} /> : null}
      <Field label="Người nhận việc" error={error}>
        <select
          className="in"
          value={adminId}
          aria-invalid={error !== undefined || undefined}
          onChange={(e) => {
            setAdminId(e.target.value);
            setError(undefined);
          }}
        >
          <option value="">{admins.isPending ? LIST_LOADING_TEXT : "Chọn quản trị viên"}</option>
          {choices.map((a) => (
            <option key={a.id} value={a.id}>
              {a.full_name} ({a.email}){a.id === me?.id ? " (bạn)" : ""}
            </option>
          ))}
        </select>
      </Field>
      <ButtonRow>
        <Button variant="primary" pending={update.pending && action === "assign"} disabled={update.pending} onClick={assign}>
          Phân công
        </Button>
        {me && ticket.assigned_admin_id !== me.id ? (
          <Button
            pending={update.pending && action === "self"}
            disabled={update.pending}
            onClick={() => {
              setAction("self");
              void update.run({ assigned_admin_id: me.id });
            }}
          >
            Giao cho tôi
          </Button>
        ) : null}
        {ticket.assigned_admin_id ? (
          <Button
            pending={update.pending && action === "unassign"}
            disabled={update.pending}
            onClick={() => {
              setAction("unassign");
              void update.run({ unassign: true });
            }}
          >
            Bỏ phân công
          </Button>
        ) : null}
      </ButtonRow>
    </Card>
  );
}
