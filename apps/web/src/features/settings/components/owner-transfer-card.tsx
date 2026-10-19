import { useState } from "react";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import type { operations } from "../../../shared/api/schema";
import { useCommit } from "../../../shared/hooks/use-commit";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";

export const OWNER_TRANSFER_SUBMIT = "Gửi lời mời chuyển quyền";
export const OWNER_TRANSFER_CONFIRM_TITLE = "Xác nhận chuyển quyền chủ cửa hàng";
const EMAIL_ERROR = "Nhập email hợp lệ của người nhận.";
const NAME_REQUIRED = "Nhập họ tên người nhận.";

type TransferBody = operations["requestOwnerTransfer"]["requestBody"]["content"]["application/json"];

/** Notice kept on the card after an invitation went out; the API has no read of pending invitations. */
function ownerTransferSentText(email: string): string {
  return `Đã gửi lời mời chuyển quyền tới ${email}. Người nhận cần mở liên kết trong email trong 72 giờ để chấp nhận.`;
}

function TransferForm({ onSent }: { onSent: (email: string) => void }) {
  const client = useApiClient();
  const [email, setEmail] = useState("");
  const [fullName, setFullName] = useState("");
  const [errors, setErrors] = useState<{ email?: string; full_name?: string }>({});
  // The invitation the confirmation is open for.
  const [confirming, setConfirming] = useState<TransferBody | null>(null);

  const transfer = useCommit({
    mutationFn: (body: TransferBody) => client.post("/owner-transfer", body),
    success: (_r, body) => `Đã gửi lời mời chuyển quyền tới ${body.email}.`,
    after: (_r, body) => {
      setConfirming(null);
      onSent(body.email);
      setEmail("");
      setFullName("");
    },
    setFieldError: (field, message) => {
      if (field !== "email" && field !== "full_name") return;
      // The refusal belongs next to the field, where it can be corrected.
      setConfirming(null);
      setErrors((cur) => ({ ...cur, [field]: message }));
    },
  });

  const submit = () => {
    const next: { email?: string; full_name?: string } = {};
    const e = email.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e)) next.email = EMAIL_ERROR;
    if (fullName.trim() === "") next.full_name = NAME_REQUIRED;
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    transfer.resetKey();
    setConfirming({ email: e, full_name: fullName.trim() });
  };

  return (
    <>
      <FormGrid>
        <Field label="Email người nhận" error={errors.email}>
          <Input type="email" autoComplete="off" value={email} onChange={(ev) => setEmail(ev.target.value)} invalid={errors.email !== undefined} />
        </Field>
        <Field label="Họ tên người nhận" error={errors.full_name}>
          <Input value={fullName} maxLength={200} onChange={(ev) => setFullName(ev.target.value)} invalid={errors.full_name !== undefined} />
        </Field>
      </FormGrid>
      <ButtonRow style={{ marginTop: 12 }}>
        <Button variant="danger" onClick={submit}>
          {OWNER_TRANSFER_SUBMIT}
        </Button>
      </ButtonRow>
      <CommitDialog
        open={confirming !== null}
        onClose={() => setConfirming(null)}
        title={OWNER_TRANSFER_CONFIRM_TITLE}
        sub={confirming ? `Gửi lời mời nhận quyền chủ cửa hàng tới ${confirming.email}.` : undefined}
        onSubmit={() => (confirming ? void transfer.run(confirming) : undefined)}
        submitLabel={OWNER_TRANSFER_SUBMIT}
        submitVariant="danger"
        pending={transfer.pending}
        pendingLabel="Đang gửi…"
      >
        <ImpactSummary
          items={[
            { label: "Người nhận", value: confirming ? `${confirming.full_name} · ${confirming.email}` : "—" },
            { label: "Thời hạn", value: "Người nhận có 72 giờ để mở liên kết trong email và chấp nhận" },
            { label: "Sau khi chấp nhận", value: "Người nhận trở thành chủ cửa hàng; tài khoản của bạn bị vô hiệu và đăng xuất trên mọi thiết bị" },
          ]}
        />
      </CommitDialog>
    </>
  );
}

/**
 * "Chuyển quyền chủ cửa hàng": e-mails another person a link to take the store over. Sending is
 * the whole flow; the last address sent to is remembered only while this screen stays open.
 */
export function OwnerTransferCard() {
  const [sentTo, setSentTo] = useState<string | null>(null);
  return (
    <Card title="Chuyển quyền chủ cửa hàng">
      <p className="hint">
        Người nhận nhận email có liên kết chấp nhận, hiệu lực 72 giờ.
      </p>
      {sentTo ? (
        <Callout tone="ok" role="status" style={{ marginBottom: 12 }}>
          {ownerTransferSentText(sentTo)}
        </Callout>
      ) : null}
      <RequireRecentAuth text="Chuyển quyền chủ cửa hàng cần xác nhận lại mật khẩu.">
        <TransferForm onSent={setSentTo} />
      </RequireRecentAuth>
    </Card>
  );
}
