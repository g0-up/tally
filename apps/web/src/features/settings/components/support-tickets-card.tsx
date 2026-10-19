import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { isApiError } from "../../../shared/api/api-error";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Field } from "../../../shared/ui/field";
import { Input, Textarea } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import {
  POSSIBLE_PASSWORD,
  TICKET_BODY_MAX,
  TICKET_PRIORITIES,
  TICKET_SUBJECT_MAX,
  ticketMessageSchema,
  supportTicketViewSchema,
  useSupportTicket,
  useSupportTickets,
  type SupportTicket,
  type TicketPriority,
} from "../api/support";
import { settingsKeys } from "../api/tenant-settings";
import { TICKET_PRIORITY, TICKET_STATUS } from "../labels";
import { LoadError } from "./load-error";

export const NEW_TICKET_LABEL = "Tạo yêu cầu";
export const SEND_TICKET_LABEL = "Gửi yêu cầu";
export const SEND_ANYWAY_LABEL = "Vẫn gửi";
export const SEND_REPLY_LABEL = "Gửi trả lời";
export const TICKET_SENT = "Đã gửi yêu cầu hỗ trợ.";
export const REPLY_SENT = "Đã gửi trả lời.";
/** Lead of a refused ticket or reply: nothing is posted to the books, so not "Chưa ghi sổ.". */
export const NOT_SENT = "Chưa gửi.";
export const POSSIBLE_PASSWORD_WARNING =
  "Nội dung có thể chứa mật khẩu hoặc mã xác thực. Nhân viên hỗ trợ không bao giờ hỏi mật khẩu; hãy xóa phần đó. Nếu chắc chắn không phải mật khẩu, bấm Vẫn gửi.";
const SUBJECT_REQUIRED = "Nhập tiêu đề.";
const BODY_REQUIRED = "Nhập nội dung.";

const isPossiblePassword = (e: unknown) => isApiError(e) && e.code === POSSIBLE_PASSWORD;

const isFinished = (t: SupportTicket) => t.status === "resolved" || t.status === "closed";

interface TicketBody {
  subject: string;
  body: string;
  priority: TicketPriority;
  acknowledge_warning: boolean;
}

function NewTicketDialog({ onClose }: { onClose: () => void }) {
  const client = useApiClient();
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [priority, setPriority] = useState<TicketPriority>("normal");
  const [errors, setErrors] = useState<{ subject?: string; body?: string }>({});
  // The text the possible-password warning is about; editing it takes the warning away.
  const [sent, setSent] = useState<string | null>(null);

  const create = useCommit({
    mutationFn: (b: TicketBody) => client.post("/support-tickets", b, { schema: supportTicketViewSchema }),
    failedLead: NOT_SENT,
    success: (t) => (
      <>
        {TICKET_SENT} Số yêu cầu <b>{t.number}</b>.
      </>
    ),
    invalidate: [settingsKeys.supportTickets()],
    after: onClose,
    setFieldError: (field, message) => {
      if (field === "subject" || field === "body") setErrors((cur) => ({ ...cur, [field]: message }));
    },
  });

  const send = (acknowledge: boolean) => {
    const next: { subject?: string; body?: string } = {};
    if (subject.trim() === "") next.subject = SUBJECT_REQUIRED;
    if (body.trim() === "") next.body = BODY_REQUIRED;
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    setSent(`${subject}\n${body}`);
    void create.run({ subject: subject.trim(), body: body.trim(), priority, acknowledge_warning: acknowledge });
  };
  const warned = isPossiblePassword(create.error) && sent === `${subject}\n${body}`;

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={NEW_TICKET_LABEL}
      sub="Mô tả việc cần hỗ trợ. Không gửi mật khẩu hay mã xác thực."
      onSubmit={() => send(false)}
      submitLabel={SEND_TICKET_LABEL}
      submitVariant="navy"
      pending={create.pending}
      extra={
        warned ? (
          <Button variant="line" disabled={create.pending} onClick={() => send(true)}>
            {SEND_ANYWAY_LABEL}
          </Button>
        ) : null
      }
    >
      {warned ? (
        <Callout tone="warn" role="alert" style={{ marginBottom: 10 }}>
          {POSSIBLE_PASSWORD_WARNING}
        </Callout>
      ) : null}
      <Field label="Tiêu đề" error={errors.subject}>
        <Input value={subject} maxLength={TICKET_SUBJECT_MAX} onChange={(e) => setSubject(e.target.value)} invalid={errors.subject !== undefined} />
      </Field>
      <Field label="Nội dung" error={errors.body}>
        <Textarea rows={6} value={body} maxLength={TICKET_BODY_MAX} onChange={(e) => setBody(e.target.value)} invalid={errors.body !== undefined} />
      </Field>
      <Field label="Mức ưu tiên">
        <select className="in" value={priority} onChange={(e) => setPriority(e.target.value as TicketPriority)}>
          {TICKET_PRIORITIES.map((p) => (
            <option key={p} value={p}>
              {TICKET_PRIORITY[p]}
            </option>
          ))}
        </select>
      </Field>
    </CommitDialog>
  );
}

interface ReplyBody {
  body: string;
  acknowledge_warning: boolean;
}

function TicketDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const client = useApiClient();
  const ticket = useSupportTicket(id);
  const [reply, setReply] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [sent, setSent] = useState<string | null>(null);

  const send = useCommit({
    mutationFn: (b: ReplyBody) => client.post(`/support-tickets/${encodeURIComponent(id)}/messages`, b, { schema: ticketMessageSchema }),
    failedLead: NOT_SENT,
    success: () => REPLY_SENT,
    invalidate: [settingsKeys.supportTickets()],
    after: () => setReply(""),
  });

  const submit = (acknowledge: boolean) => {
    if (reply.trim() === "") {
      setError(BODY_REQUIRED);
      return;
    }
    setError(undefined);
    setSent(reply);
    void send.run({ body: reply.trim(), acknowledge_warning: acknowledge });
  };
  const warned = isPossiblePassword(send.error) && sent === reply;

  const view = ticket.data;
  const open = view !== undefined && !isFinished(view);

  return (
    <CommitDialog
      open
      wide
      onClose={onClose}
      title={view ? `${view.number} · ${view.subject}` : "Yêu cầu hỗ trợ"}
      {...(view ? { sub: `${TICKET_STATUS[view.status].label} · ${TICKET_PRIORITY[view.priority]}` } : {})}
      {...(open ? { onSubmit: () => submit(false) } : {})}
      submitLabel={SEND_REPLY_LABEL}
      submitVariant="navy"
      pending={send.pending}
      cancelLabel="Đóng"
      extra={
        open && warned ? (
          <Button variant="line" disabled={send.pending} onClick={() => submit(true)}>
            {SEND_ANYWAY_LABEL}
          </Button>
        ) : null
      }
    >
      {ticket.isPending ? (
        <ListLoading />
      ) : ticket.error ? (
        <LoadError what="yêu cầu hỗ trợ" error={ticket.error} />
      ) : (
        <>
          <ol aria-label="Trao đổi" style={{ listStyle: "none", padding: 0, margin: 0, display: "grid", gap: 10 }}>
            {ticket.data.messages.map((m) => (
              <li key={m.id}>
                <b>{m.author_type === "owner" ? "Bạn" : "Nhân viên hỗ trợ"}</b> <span className="hint">{fdt(m.created_at)}</span>
                <p style={{ whiteSpace: "pre-line", margin: "4px 0 0" }}>{m.body}</p>
              </li>
            ))}
          </ol>
          {open ? (
            <div style={{ marginTop: 14 }}>
              {warned ? (
                <Callout tone="warn" role="alert" style={{ marginBottom: 10 }}>
                  {POSSIBLE_PASSWORD_WARNING}
                </Callout>
              ) : null}
              <Field label="Trả lời" error={error}>
                <Textarea rows={4} value={reply} maxLength={TICKET_BODY_MAX} onChange={(e) => setReply(e.target.value)} invalid={error !== undefined} />
              </Field>
            </div>
          ) : (
            <p className="hint" style={{ marginTop: 14 }}>
              Yêu cầu đã kết thúc. Tạo yêu cầu mới nếu cần hỗ trợ tiếp.
            </p>
          )}
        </>
      )}
    </CommitDialog>
  );
}

/** "Yêu cầu hỗ trợ": the store's tickets, a new one, and the conversation of each. */
export function SupportTicketsCard() {
  const tickets = useSupportTickets();
  const [creating, setCreating] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);

  const columns: ReadonlyArray<Column<SupportTicket>> = [
    {
      key: "no",
      header: "Yêu cầu",
      cell: (t) => (
        <CellText
          main={
            <button type="button" className="lnk" onClick={() => setOpenId(t.id)}>
              {t.number}
            </button>
          }
          sub={t.subject}
        />
      ),
    },
    { key: "priority", header: "Mức ưu tiên", cell: (t) => TICKET_PRIORITY[t.priority] },
    {
      key: "status",
      header: "Trạng thái",
      cell: (t) => {
        const s = TICKET_STATUS[t.status];
        return <StatusPill {...s} />;
      },
    },
    { key: "updated", header: "Cập nhật", cell: (t) => fdt(t.updated_at) },
  ];

  return (
    <Card
      title="Yêu cầu hỗ trợ"
      actions={
        <Button variant="navy" size="sm" onClick={() => setCreating(true)}>
          {NEW_TICKET_LABEL}
        </Button>
      }
    >
      {tickets.isPending ? (
        <ListLoading />
      ) : tickets.error ? (
        <LoadError what="yêu cầu hỗ trợ" error={tickets.error} />
      ) : (
        <DataTable caption="Yêu cầu hỗ trợ" columns={columns} rows={tickets.data} rowKey={(t) => t.id} flat empty="Chưa có yêu cầu hỗ trợ." />
      )}
      {creating ? <NewTicketDialog onClose={() => setCreating(false)} /> : null}
      {openId ? <TicketDialog id={openId} onClose={() => setOpenId(null)} /> : null}
    </Card>
  );
}
