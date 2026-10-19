import { useId, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import {
  grantableTicket,
  SUPPORT_GRANT_HOURS,
  SUPPORT_VIEW_MODULES,
  supportSessionSchema,
  useSupportSessions,
  useSupportTickets,
  type SupportSession,
  type SupportTicket,
  type SupportViewModule,
} from "../api/support";
import { settingsKeys } from "../api/tenant-settings";
import { SUPPORT_MODULE } from "../labels";
import { LoadError } from "./load-error";

export const SUPPORT_ACCESS_TEXT = "Nhân viên hỗ trợ chỉ truy cập khi chủ cửa hàng đồng ý, có phạm vi và thời hạn, mặc định chỉ đọc. Không yêu cầu gửi mật khẩu.";
export const SUPPORT_GRANT_LABEL = "Cấp quyền hỗ trợ 2 giờ (chỉ đọc)";
export const SUPPORT_GRANTED = "Đã cấp quyền hỗ trợ có thời hạn.";
export const SUPPORT_REVOKED = "Đã thu hồi.";
export const NO_GRANTABLE_TICKET = "Cần một yêu cầu hỗ trợ đang mở đã có nhân viên phụ trách.";
const MODULES_REQUIRED = "Chọn ít nhất một phần được xem.";

interface GrantBody {
  ticket_id: string;
  modules: SupportViewModule[];
  hours: number;
}

/** The session support can use now; the API reports a session past its end as expired. */
function liveSession(sessions: readonly SupportSession[]): SupportSession | null {
  return sessions.find((s) => s.state === "active" && s.revoked_at === null) ?? null;
}

function GrantDialog({ open, tickets, onClose }: { open: boolean; tickets: readonly SupportTicket[]; onClose: () => void }) {
  const client = useApiClient();
  const [ticketId, setTicketId] = useState(tickets[0]?.id ?? "");
  const [modules, setModules] = useState<ReadonlySet<SupportViewModule>>(new Set(SUPPORT_VIEW_MODULES));
  const [error, setError] = useState<string | undefined>();
  const scopeId = useId();
  const scopeErrorId = useId();

  const grant = useCommit({
    mutationFn: (body: GrantBody) => client.post("/support-sessions", body, { schema: supportSessionSchema }),
    success: () => SUPPORT_GRANTED,
    invalidate: [settingsKeys.supportSessions()],
    after: onClose,
  });

  const toggle = (m: SupportViewModule, on: boolean) =>
    setModules((cur) => {
      const next = new Set(cur);
      if (on) next.add(m);
      else next.delete(m);
      return next;
    });

  const submit = () => {
    if (modules.size === 0) {
      setError(MODULES_REQUIRED);
      return;
    }
    setError(undefined);
    void grant.run({ ticket_id: ticketId, modules: SUPPORT_VIEW_MODULES.filter((m) => modules.has(m)), hours: SUPPORT_GRANT_HOURS });
  };

  return (
    <CommitDialog
      open={open}
      onClose={onClose}
      title={SUPPORT_GRANT_LABEL}
      sub={`Nhân viên phụ trách yêu cầu được xem các phần đã chọn trong ${SUPPORT_GRANT_HOURS} giờ, không sửa được dữ liệu. Bạn có thể thu hồi bất cứ lúc nào.`}
      onSubmit={submit}
      submitLabel="Cấp quyền"
      submitVariant="navy"
      submitDisabled={ticketId === ""}
      pending={grant.pending}
    >
      <Field label="Yêu cầu hỗ trợ">
        <select className="in" value={ticketId} onChange={(e) => setTicketId(e.target.value)}>
          {tickets.map((t) => (
            <option key={t.id} value={t.id}>
              {t.number} · {t.subject}
            </option>
          ))}
        </select>
      </Field>
      {/* A group of labelled checkboxes cannot sit inside the field's <label>. */}
      <div className="f">
        <span id={scopeId}>Phạm vi xem</span>
        <div
          role="group"
          aria-labelledby={scopeId}
          aria-describedby={error === undefined ? undefined : scopeErrorId}
          style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))", gap: 4 }}
        >
          {SUPPORT_VIEW_MODULES.map((m) => (
            <Checkbox key={m} label={SUPPORT_MODULE[m]} checked={modules.has(m)} onChange={(e) => toggle(m, e.target.checked)} />
          ))}
        </div>
        {error === undefined ? null : (
          <span className="err" role="alert" id={scopeErrorId}>
            {error}
          </span>
        )}
      </div>
    </CommitDialog>
  );
}

/** "Truy cập hỗ trợ": the owner lets support read the store for a while, and can end it early. */
export function SupportAccessCard() {
  const client = useApiClient();
  const sessions = useSupportSessions();
  const tickets = useSupportTickets();
  const [granting, setGranting] = useState(false);
  const noTicketId = useId();

  const revoke = useCommit({
    mutationFn: (id: string) => client.post(`/support-sessions/${encodeURIComponent(id)}:revoke`, undefined, { schema: supportSessionSchema }),
    success: () => SUPPORT_REVOKED,
    invalidate: [settingsKeys.supportSessions()],
  });

  const grantable = (tickets.data ?? []).filter(grantableTicket);
  const live = sessions.data ? liveSession(sessions.data) : null;
  const noTicket = tickets.data !== undefined && grantable.length === 0;
  const liveTicket = live ? tickets.data?.find((t) => t.id === live.ticket_id) : undefined;

  return (
    <Card title="Truy cập hỗ trợ">
      <p>{SUPPORT_ACCESS_TEXT}</p>
      <ButtonRow>
        <Button variant="navy" size="sm" disabled={grantable.length === 0} aria-describedby={noTicket ? noTicketId : undefined} onClick={() => setGranting(true)}>
          {SUPPORT_GRANT_LABEL}
        </Button>
      </ButtonRow>
      {noTicket ? (
        <p className="hint" id={noTicketId}>
          {NO_GRANTABLE_TICKET}
        </p>
      ) : null}
      {sessions.error ? <LoadError what="phiên hỗ trợ" error={sessions.error} /> : null}
      {live ? (
        <Callout tone="ok" style={{ marginTop: 10 }}>
          Phiên hỗ trợ {liveTicket?.number ?? live.id.slice(0, 8)} đang mở đến {fdt(live.expires_at)} · chỉ đọc ·{" "}
          <Button size="sm" pending={revoke.pending} pendingLabel="Đang thu hồi…" onClick={() => void revoke.run(live.id)}>
            Thu hồi
          </Button>
        </Callout>
      ) : null}
      {granting ? <GrantDialog open tickets={grantable} onClose={() => setGranting(false)} /> : null}
    </Card>
  );
}
