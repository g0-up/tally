import { useId, useRef, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { recoveryCodesSchema, type TotpEnrolment } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { copyText } from "./copy-text";

export const MFA_SETUP_TITLE = "Bật xác thực 2 lớp";
export const MFA_ENABLED_TOAST = "Đã bật xác thực 2 lớp. Các thiết bị khác đã được đăng xuất.";
export const RECOVERY_CODES_NOTICE = "Lưu các mã khôi phục này ở nơi an toàn. Mỗi mã dùng được một lần khi không có ứng dụng xác thực. Mã chỉ hiện lần này.";
export const RECOVERY_CODES_TITLE = "Mã khôi phục";
export const RECOVERY_CODES_SAVED = "Tôi đã lưu mã";
export const RECOVERY_CODES_COPIED = "Đã sao chép mã khôi phục.";
export const RECOVERY_CODES_FILE = "tally-ma-khoi-phuc.txt";
export const RECOVERY_CODES_UNSAVED = "Đánh dấu “Tôi đã lưu mã” trước khi đóng. Sau khi đóng không xem lại được các mã này.";
export const MFA_SECRET_COPIED = "Đã sao chép khóa thiết lập.";
const CODE_REQUIRED = "Nhập mã 6 số trong ứng dụng xác thực.";

/**
 * The recovery codes, shown once. Esc, the scrim, "×" and "Đóng" are all refused until the owner
 * ticks that the codes are saved, since closing drops them for good.
 */
function RecoveryCodesDialog({ codes, onClose }: { codes: readonly string[]; onClose: () => void }) {
  const [saved, setSaved] = useState(false);
  const [refused, setRefused] = useState(false);
  const savedRef = useRef<HTMLInputElement>(null);
  const warningId = useId();
  const text = `${codes.join("\n")}\n`;

  const close = () => {
    if (saved) {
      onClose();
      return;
    }
    setRefused(true);
    savedRef.current?.focus();
  };

  return (
    <CommitDialog open onClose={close} title={RECOVERY_CODES_TITLE} cancelLabel="Đóng">
      <Callout tone="warn">{RECOVERY_CODES_NOTICE}</Callout>
      <ol aria-label={RECOVERY_CODES_TITLE} style={{ columns: 2, marginTop: 12 }}>
        {codes.map((c) => (
          <li key={c}>
            <code>{c}</code>
          </li>
        ))}
      </ol>
      <ButtonRow style={{ marginBottom: 12 }}>
        <Button size="sm" onClick={() => copyText(text, RECOVERY_CODES_COPIED, "Hãy tải tệp .txt hoặc chép tay các mã.")}>
          Sao chép
        </Button>
        <a className="btn btn-line btn-sm" href={`data:text/plain;charset=utf-8,${encodeURIComponent(text)}`} download={RECOVERY_CODES_FILE}>
          Tải .txt
        </a>
      </ButtonRow>
      <Checkbox
        ref={savedRef}
        label={RECOVERY_CODES_SAVED}
        checked={saved}
        onChange={(e) => setSaved(e.target.checked)}
        aria-describedby={refused && !saved ? warningId : undefined}
      />
      {refused && !saved ? (
        <p className="err-text" role="alert" id={warningId}>
          {RECOVERY_CODES_UNSAVED}
        </p>
      ) : null}
    </CommitDialog>
  );
}

/**
 * Confirms a started TOTP enrolment with a code from the authenticator app, then shows the ten
 * recovery codes once. Closing the dialog drops them from memory.
 */
export function MfaSetupDialog({ enrolment, onClose }: { enrolment: TotpEnrolment | null; onClose: () => void }) {
  const client = useApiClient();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const verify = useCommit({
    mutationFn: (value: string) => client.post("/auth/mfa/totp/verify", { code: value }, { schema: recoveryCodesSchema }),
    success: () => MFA_ENABLED_TOAST,
    invalidate: [settingsKeys.owner(), settingsKeys.sessions(), platformKeys.me()],
    after: (r) => setRecoveryCodes(r.recovery_codes),
  });

  const close = () => {
    setCode("");
    setError(undefined);
    setRecoveryCodes(null);
    onClose();
  };

  const submit = () => {
    const value = code.replace(/\s+/g, "");
    if (!/^\d{6}$/.test(value)) {
      setError(CODE_REQUIRED);
      return;
    }
    setError(undefined);
    void verify.run(value);
  };

  if (recoveryCodes) return <RecoveryCodesDialog codes={recoveryCodes} onClose={close} />;

  return (
    <CommitDialog
      open={enrolment !== null}
      onClose={close}
      title={MFA_SETUP_TITLE}
      sub="Thêm khóa dưới đây vào ứng dụng xác thực (Google Authenticator, Microsoft Authenticator…), rồi nhập mã 6 số ứng dụng hiện ra."
      onSubmit={submit}
      submitLabel={MFA_SETUP_TITLE}
      pending={verify.pending}
    >
      {enrolment ? (
        <>
          <div style={{ display: "flex", gap: 8, alignItems: "flex-end" }}>
            <Field label="Khóa thiết lập">
              <Input readOnly value={enrolment.secret} />
            </Field>
            <Button size="sm" aria-label="Sao chép khóa thiết lập" onClick={() => copyText(enrolment.secret, MFA_SECRET_COPIED, "Hãy chọn và sao chép khóa.")}>
              Sao chép
            </Button>
          </div>
          {/* Outside the field's <label>: a link there would trigger the label and lengthen the field's name. */}
          <p className="hint">
            <a href={enrolment.otpauth_uri}>Mở bằng ứng dụng xác thực trên máy này</a>
          </p>
          <Field label="Mã xác thực" error={error}>
            <Input inputMode="numeric" autoComplete="one-time-code" maxLength={8} value={code} onChange={(e) => setCode(e.target.value)} invalid={error !== undefined} autoFocus />
          </Field>
        </>
      ) : null}
    </CommitDialog>
  );
}
