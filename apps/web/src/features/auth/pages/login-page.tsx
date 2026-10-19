import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router";
import { safeNextPath } from "../../../app/guards/login-redirect";
import { useHostContext } from "../../../app/host-context";
import { HOME_PATH } from "../../../app/menu";
import { isApiError } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { useLogin } from "../api/mutations";
import { FormAlert, FormNotice } from "../components/form-alert";
import { AUTH_ERROR_CODES, applyAuthError } from "../lib/auth-errors";
import { readLoginNotice, type MfaState } from "../lib/auth-state";
import { loginSchema, type LoginValues } from "../schemas";

/** Root-host page URL ("https://tally.vn/signup"), or null while the root URL is unknown. */
function rootPage(rootUrl: string | undefined, path: string): string | null {
  if (!rootUrl) return null;
  try {
    return new URL(path, rootUrl).toString();
  } catch {
    return null;
  }
}

/**
 * Store sign-in (tenant host). The store's name is not shown before sign-in, so an unknown store
 * address looks exactly like a real one. A wrong password clears the password field only.
 */
export default function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const next = params.get("next");
  const host = useHostContext();
  const login = useLogin();
  const [formError, setFormError] = useState<string | null>(null);
  const notice = formError ? null : readLoginNotice(location.state);
  const {
    register,
    handleSubmit,
    setError,
    resetField,
    setFocus,
    formState: { errors },
  } = useForm<LoginValues>({ resolver: zodResolver(loginSchema), defaultValues: { login: "", password: "" } });

  const onSubmit = handleSubmit(async (values) => {
    setFormError(null);
    try {
      const result = await login.mutateAsync(values);
      if (result.status === "mfa_required") {
        const state: MfaState = { challenge: result.challenge, next };
        await navigate("/mfa", { state });
        return;
      }
      await navigate(safeNextPath(next, HOME_PATH), { replace: true });
    } catch (e) {
      setFormError(applyAuthError(e, ["login", "password"], setError));
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.invalidCredentials) {
        resetField("password");
        setFocus("password");
      }
    }
  });

  const signupUrl = rootPage(host.data?.root_url, "/signup");
  const findStoreUrl = rootPage(host.data?.root_url, "/find-store");

  return (
    <>
      <h1>Đăng nhập</h1>
      <p className="auth-lead">Đăng nhập bằng email hoặc số điện thoại của chủ cửa hàng.</p>
      <FormNotice notice={notice} />
      <FormAlert message={formError} />
      <form className="auth-form" noValidate onSubmit={(e) => void onSubmit(e)}>
        <Field label="Email hoặc số điện thoại" error={errors.login?.message}>
          <Input type="text" inputMode="email" autoComplete="username" autoFocus invalid={errors.login !== undefined} {...register("login")} />
        </Field>
        <Field label="Mật khẩu" error={errors.password?.message}>
          <Input type="password" autoComplete="current-password" invalid={errors.password !== undefined} {...register("password")} />
        </Field>
        <Button variant="primary" type="submit" pending={login.isPending} pendingLabel="Đang đăng nhập…">
          Đăng nhập
        </Button>
      </form>
      <p className="auth-links">
        <Link to="/forgot-password">Quên mật khẩu?</Link>
        {findStoreUrl ? <a href={findStoreUrl}>Tìm cửa hàng của bạn</a> : null}
        {signupUrl ? <a href={signupUrl}>Đăng ký cửa hàng mới</a> : null}
      </p>
    </>
  );
}
