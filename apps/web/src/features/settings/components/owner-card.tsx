import { useState } from "react";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { totpEnrolmentSchema, useOwner, type Owner, type TotpEnrolment } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { OWNER_STATUS } from "../labels";
import { CHANGE_PASSWORD_LABEL, ChangePasswordDialog } from "./change-password-dialog";
import { LoadError } from "./load-error";
import { MFA_SETUP_TITLE, MfaSetupDialog } from "./mfa-setup-dialog";

export const MFA_DISABLE_LABEL = "Tắt xác thực 2 lớp";
export const MFA_DISABLED_TOAST = "Đã tắt xác thực 2 lớp.";

function MfaControls({ owner }: { owner: Owner }) {
  const client = useApiClient();
  const [enrolment, setEnrolment] = useState<TotpEnrolment | null>(null);
  const [confirmDisable, setConfirmDisable] = useState(false);

  const start = useCommit({
    mutationFn: () => client.post("/auth/mfa/totp", undefined, { schema: totpEnrolmentSchema }),
    after: (r) => setEnrolment(r),
  });
  const disable = useCommit({
    mutationFn: () => client.delete("/auth/mfa/totp"),
    success: () => MFA_DISABLED_TOAST,
    invalidate: [settingsKeys.owner(), platformKeys.me()],
    after: () => setConfirmDisable(false),
  });

  return (
    <>
      <ButtonRow style={{ marginTop: 12 }}>
        {owner.user.mfa_enabled ? (
          <Button
            variant="danger"
            size="sm"
            onClick={() => {
              disable.resetKey();
              setConfirmDisable(true);
            }}
          >
            {MFA_DISABLE_LABEL}
          </Button>
        ) : (
          <Button variant="navy" size="sm" pending={start.pending} pendingLabel="Đang tạo khóa…" onClick={() => void start.run()}>
            {MFA_SETUP_TITLE}
          </Button>
        )}
      </ButtonRow>
      <MfaSetupDialog enrolment={enrolment} onClose={() => setEnrolment(null)} />
      <CommitDialog
        open={confirmDisable}
        onClose={() => setConfirmDisable(false)}
        title={MFA_DISABLE_LABEL}
        sub="Sau khi tắt, đăng nhập chỉ cần mật khẩu. Các mã khôi phục cũ không còn dùng được."
        onSubmit={() => void disable.run()}
        submitLabel={MFA_DISABLE_LABEL}
        submitVariant="danger"
        pending={disable.pending}
      >
        <p>Bạn có chắc muốn tắt xác thực 2 lớp cho tài khoản {owner.user.email}?</p>
      </CommitDialog>
    </>
  );
}

/** "Chủ cửa hàng": the one account of the store, its password and its two-step sign-in. */
export function OwnerCard() {
  const owner = useOwner();
  const [changingPassword, setChangingPassword] = useState(false);
  const status = owner.data ? (OWNER_STATUS[owner.data.user.status] ?? { label: owner.data.user.status }) : null;
  return (
    <Card title="Chủ cửa hàng" actions={status ? <StatusPill {...status} /> : null}>
      {owner.isPending ? (
        <ListLoading />
      ) : owner.error ? (
        <LoadError what="tài khoản" error={owner.error} />
      ) : (
        <>
          <KeyValueGrid
            items={[
              { label: "Họ tên", value: owner.data.user.full_name },
              { label: "Cửa hàng", value: owner.data.tenant.display_name },
              { label: "Đăng nhập gần nhất", value: owner.data.user.last_login_at ? fdt(owner.data.user.last_login_at) : "—" },
              { label: "Xác thực 2 lớp", value: owner.data.user.mfa_enabled ? "Đã bật" : "Chưa bật" },
            ]}
          />
          <ButtonRow style={{ marginTop: 12 }}>
            <Button variant="line" size="sm" onClick={() => setChangingPassword(true)}>
              {CHANGE_PASSWORD_LABEL}
            </Button>
          </ButtonRow>
          {changingPassword ? <ChangePasswordDialog onClose={() => setChangingPassword(false)} /> : null}
          <div style={{ marginTop: 12 }}>
            <RequireRecentAuth text="Bật hoặc tắt xác thực 2 lớp cần xác nhận lại mật khẩu.">
              <MfaControls owner={owner.data} />
            </RequireRecentAuth>
          </div>
        </>
      )}
    </Card>
  );
}
