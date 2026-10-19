import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate, useSearchParams } from "react-router";
import { safeNextPath } from "../../../app/guards/login-redirect";
import { isApiError } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { useAdminLogin, useAdminTotp } from "../api/mutations";
import { FormAlert, FormNotice } from "../components/form-alert";
import { TotpCodeForm } from "../components/mfa-code-forms";
import { AUTH_ERROR_CODES, AUTH_MESSAGES, applyAuthError, authErrorMessage } from "../lib/auth-errors";
import type { Notice } from "../lib/auth-state";
import { adminLoginSchema, type AdminLoginValues } from "../schemas";

const ADMIN_HOME = "/admin";

/**
 * Platform admin sign-in (admin host): password, then a TOTP code, always both. The admin session
 * is separate from any store session, and nothing about stores is shown here.
 */
export default function AdminLoginPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNextPath(params.get("next"), ADMIN_HOME);
  const login = useAdminLogin();
  const totp = useAdminTotp();
  const [challenge, setChallenge] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const {
    register,
    handleSubmit,
    setError,
    resetField,
    setFocus,
    formState: { errors },
  } = useForm<AdminLoginValues>({ resolver: zodResolver(adminLoginSchema), defaultValues: { email: "", password: "" } });

  const onPassword = handleSubmit(async (values) => {
    setFormError(null);
    setNotice(null);
    try {
      const result = await login.mutateAsync(values);
      if (result.status === "mfa_required") {
        setChallenge(result.challenge);
        return;
      }
      await navigate(next, { replace: true });
    } catch (e) {
      setFormError(applyAuthError(e, ["email", "password"], setError));
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.invalidCredentials) {
        resetField("password");
        setFocus("password");
      }
    }
  });

  const onCode = async (body: { code: string }): Promise<string | null> => {
    if (challenge === null) return null;
    setFormError(null);
    try {
      const result = await totp.mutateAsync({ challenge, code: body.code });
      if (result.status === "signed_in") await navigate(next, { replace: true });
      return null;
    } catch (e) {
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.challengeInvalid) {
        setChallenge(null);
        resetField("password");
        setNotice({ message: AUTH_MESSAGES.challengeExpired, tone: "warn" });
        return null;
      }
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.invalidCode) return AUTH_MESSAGES.invalidCode;
      setFormError(authErrorMessage(e));
      return null;
    }
  };

  if (challenge !== null) {
    return (
      <>
        <h1>Xác thực hai lớp</h1>
        <p className="auth-lead">Nhập mã 6 chữ số từ ứng dụng xác thực của tài khoản quản trị.</p>
        <FormAlert message={formError} />
        <TotpCodeForm pending={totp.isPending} onSubmit={onCode} />
        <p className="auth-links">
          <Button
            variant="line"
            size="sm"
            onClick={() => {
              setChallenge(null);
              setFormError(null);
              resetField("password");
            }}
          >
            Đăng nhập lại
          </Button>
        </p>
      </>
    );
  }

  return (
    <>
      <h1>Đăng nhập quản trị</h1>
      <p className="auth-lead">Bảng điều khiển dành cho quản trị viên nền tảng. Cần mật khẩu và mã xác thực hai lớp.</p>
      <FormNotice notice={notice} />
      <FormAlert message={formError} />
      <form className="auth-form" noValidate onSubmit={(e) => void onPassword(e)}>
        <Field label="Email" error={errors.email?.message}>
          <Input type="email" autoComplete="username" autoFocus invalid={errors.email !== undefined} {...register("email")} />
        </Field>
        <Field label="Mật khẩu" error={errors.password?.message}>
          <Input type="password" autoComplete="current-password" invalid={errors.password !== undefined} {...register("password")} />
        </Field>
        <Button variant="primary" type="submit" pending={login.isPending} pendingLabel="Đang đăng nhập…">
          Tiếp tục
        </Button>
      </form>
    </>
  );
}
