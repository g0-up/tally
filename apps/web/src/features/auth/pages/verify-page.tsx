import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Link, useNavigate, useSearchParams } from "react-router";
import { isApiError } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { useVerifyEmail } from "../api/mutations";
import { FormAlert } from "../components/form-alert";
import { NewPasswordFields } from "../components/new-password-fields";
import { AUTH_ERROR_CODES, AUTH_MESSAGES, applyAuthError, authErrorMessage } from "../lib/auth-errors";
import { NOTICES, type LoginState } from "../lib/auth-state";
import { newPasswordSchema, type NewPasswordValues } from "../schemas";

/** The account behind the link has no password yet (created by the operator): one must be set. */
function needsPassword(e: unknown): boolean {
  return isApiError(e) && e.status === 422 && e.fields.password === "required";
}

/**
 * Opens from the verification e-mail (/verify?token=…). Nothing is sent until the owner presses
 * the button, so a mail scanner opening the link does not use it up. An account without a
 * password gets the new-password fields first.
 */
export default function VerifyPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const navigate = useNavigate();
  const verify = useVerifyEmail();
  const [askPassword, setAskPassword] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [linkDead, setLinkDead] = useState(false);
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<NewPasswordValues>({ resolver: zodResolver(newPasswordSchema), defaultValues: { password: "", confirm: "" } });

  const send = async (password?: string) => {
    setFormError(null);
    try {
      await verify.mutateAsync(password === undefined ? { token } : { token, password });
      const state: LoginState = { notice: NOTICES.verified };
      await navigate("/login", { replace: true, state });
    } catch (e) {
      if (!askPassword && needsPassword(e)) {
        setAskPassword(true);
        return;
      }
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.tokenInvalid) setLinkDead(true);
      setFormError(askPassword ? applyAuthError(e, ["password"], setError) : authErrorMessage(e));
    }
  };

  if (token === "" || linkDead) {
    return (
      <>
        <h1>Xác minh email</h1>
        <Callout tone="bad" role="alert">
          {token === "" ? "Liên kết xác minh không đầy đủ. Mở lại liên kết trong thư, hoặc sao chép toàn bộ địa chỉ vào trình duyệt." : AUTH_MESSAGES.tokenInvalid}
        </Callout>
        <p className="auth-links">
          <Link to="/login">Về trang đăng nhập</Link>
          <Link to="/forgot-password">Gửi lại liên kết qua “Quên mật khẩu”</Link>
        </p>
      </>
    );
  }

  if (askPassword) {
    return (
      <>
        <h1>Đặt mật khẩu</h1>
        <p className="auth-lead">Tài khoản chưa có mật khẩu. Đặt mật khẩu để hoàn tất xác minh email.</p>
        <FormAlert message={formError} />
        <form className="auth-form" noValidate onSubmit={(e) => void handleSubmit((v) => send(v.password))(e)}>
          <NewPasswordFields
            password={register("password")}
            confirm={register("confirm")}
            passwordError={errors.password?.message}
            confirmError={errors.confirm?.message}
            autoFocus
          />
          <Button variant="primary" type="submit" pending={verify.isPending} pendingLabel="Đang xác minh…">
            Đặt mật khẩu và xác minh
          </Button>
        </form>
      </>
    );
  }

  return (
    <>
      <h1>Xác minh email</h1>
      <p className="auth-lead">Bấm nút dưới đây để xác nhận email này là của bạn và kích hoạt tài khoản.</p>
      <FormAlert message={formError} />
      <Button variant="primary" autoFocus pending={verify.isPending} pendingLabel="Đang xác minh…" onClick={() => void send()}>
        Xác minh email
      </Button>
    </>
  );
}
