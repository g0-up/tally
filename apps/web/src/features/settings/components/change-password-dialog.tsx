import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { useApiClient } from "../../../shared/api/api-context";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { NewPasswordFields, newPasswordSchema, type NewPasswordValues } from "../../auth";
import { revokedSessionsSchema } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";

export const CHANGE_PASSWORD_LABEL = "Đổi mật khẩu";

/** "Đã đổi mật khẩu." and how many other devices the change signed out. */
function passwordChangedText(revoked: number): string {
  return revoked > 0 ? `Đã đổi mật khẩu. ${revoked} thiết bị khác đã bị đăng xuất.` : "Đã đổi mật khẩu.";
}

/**
 * "Đổi mật khẩu": the new password typed twice. The API asks for the current password first
 * (reauth_required), signs out every other session and keeps this one. Mount per opening.
 */
export function ChangePasswordDialog({ onClose }: { onClose: () => void }) {
  const client = useApiClient();
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<NewPasswordValues>({ resolver: zodResolver(newPasswordSchema), defaultValues: { password: "", confirm: "" } });
  const change = useCommit({
    mutationFn: (password: string) => client.post("/auth/password", { password }, { schema: revokedSessionsSchema }),
    success: (r) => passwordChangedText(r.revoked),
    invalidate: [settingsKeys.sessions(), settingsKeys.owner(), platformKeys.me()],
    after: onClose,
    setFieldError: (field, message) => {
      if (field === "password") setError("password", { message });
    },
  });

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={CHANGE_PASSWORD_LABEL}
      sub="Các thiết bị khác sẽ bị đăng xuất; thiết bị này vẫn giữ đăng nhập."
      submitLabel={CHANGE_PASSWORD_LABEL}
      onSubmit={() => void handleSubmit(({ password }) => change.run(password))()}
      pending={change.pending}
    >
      <NewPasswordFields password={register("password")} confirm={register("confirm")} passwordError={errors.password?.message} confirmError={errors.confirm?.message} autoFocus />
    </CommitDialog>
  );
}
