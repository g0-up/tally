import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Link, useNavigate, useSearchParams } from "react-router";
import { isApiError } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { useResetPassword } from "../api/mutations";
import { FormAlert } from "../components/form-alert";
import { NewPasswordFields } from "../components/new-password-fields";
import { AUTH_ERROR_CODES, AUTH_MESSAGES, applyAuthError } from "../lib/auth-errors";
import { NOTICES, type LoginState } from "../lib/auth-state";
import { newPasswordSchema, type NewPasswordValues } from "../schemas";

/**
 * Opens from the reset e-mail (/reset-password?token=…). The new password signs out every session
 * of the account, so the owner lands on /login afterwards. A link without a token (the "account
 * already exists" e-mail) or a used one points to "Quên mật khẩu" for a fresh link.
 */
export default function ResetPasswordPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const navigate = useNavigate();
  const reset = useResetPassword();
  const [formError, setFormError] = useState<string | null>(null);
  const [linkDead, setLinkDead] = useState(false);
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<NewPasswordValues>({ resolver: zodResolver(newPasswordSchema), defaultValues: { password: "", confirm: "" } });

  const onSubmit = handleSubmit(async ({ password }) => {
    setFormError(null);
    try {
      await reset.mutateAsync({ token, password });
      const state: LoginState = { notice: NOTICES.passwordReset };
      await navigate("/login", { replace: true, state });
    } catch (e) {
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.tokenInvalid) {
        setLinkDead(true);
        return;
      }
      setFormError(applyAuthError(e, ["password"], setError));
    }
  });

  if (token === "" || linkDead) {
    return (
      <>
        <h1>Đặt lại mật khẩu</h1>
        <Callout tone={linkDead ? "bad" : "info"} role={linkDead ? "alert" : "status"}>
          {linkDead ? AUTH_MESSAGES.tokenInvalid : "Để đặt lại mật khẩu, yêu cầu một liên kết mới qua “Quên mật khẩu”."}
        </Callout>
        <p className="auth-links">
          <Link to="/forgot-password">Gửi liên kết mới</Link>
          <Link to="/login">Về trang đăng nhập</Link>
        </p>
      </>
    );
  }

  return (
    <>
      <h1>Đặt lại mật khẩu</h1>
      <p className="auth-lead">Đặt mật khẩu mới cho tài khoản. Mọi thiết bị đang đăng nhập sẽ bị đăng xuất.</p>
      <FormAlert message={formError} />
      <form className="auth-form" noValidate onSubmit={(e) => void onSubmit(e)}>
        <NewPasswordFields
          password={register("password")}
          confirm={register("confirm")}
          passwordError={errors.password?.message}
          confirmError={errors.confirm?.message}
          autoFocus
        />
        <Button variant="primary" type="submit" pending={reset.isPending} pendingLabel="Đang lưu…">
          Lưu mật khẩu mới
        </Button>
      </form>
      <p className="auth-links">
        <Link to="/login">Về trang đăng nhập</Link>
      </p>
    </>
  );
}
