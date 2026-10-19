import { useState } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { safeNextPath } from "../../../app/guards/login-redirect";
import { HOME_PATH } from "../../../app/menu";
import { isApiError } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { useVerifyMfa } from "../api/mutations";
import { FormAlert } from "../components/form-alert";
import { RecoveryCodeForm, TotpCodeForm } from "../components/mfa-code-forms";
import { AUTH_ERROR_CODES, AUTH_MESSAGES, authErrorMessage } from "../lib/auth-errors";
import { readMfaState, type LoginState } from "../lib/auth-state";

const UNEXPECTED_STEP = "Không hoàn tất được đăng nhập. Đăng nhập lại từ đầu.";

/**
 * Second sign-in step when the owner has TOTP turned on: a 6-digit code from the authenticator
 * app, or one of the recovery codes. The challenge comes from the password step; without it (page
 * opened directly, or expired) the owner goes back to /login.
 */
export default function MfaPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const mfa = readMfaState(location.state);
  const verify = useVerifyMfa();
  const [useRecovery, setUseRecovery] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  if (!mfa) return <Navigate to="/login" replace />;
  const loginPath = mfa.next ? `/login?next=${encodeURIComponent(mfa.next)}` : "/login";

  /** Sends one code; returns the field message to show, or null when the error went elsewhere. */
  const submit = async (body: { code: string } | { recovery_code: string }): Promise<string | null> => {
    setFormError(null);
    try {
      const result = await verify.mutateAsync({ challenge: mfa.challenge, ...body });
      if (result.status !== "signed_in") {
        setFormError(UNEXPECTED_STEP);
        return null;
      }
      await navigate(safeNextPath(mfa.next, HOME_PATH), { replace: true });
      return null;
    } catch (e) {
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.challengeInvalid) {
        const state: LoginState = { notice: AUTH_MESSAGES.challengeExpired, tone: "warn" };
        await navigate(loginPath, { replace: true, state });
        return null;
      }
      if (isApiError(e) && e.code === AUTH_ERROR_CODES.invalidCode) return AUTH_MESSAGES.invalidCode;
      setFormError(authErrorMessage(e));
      return null;
    }
  };

  return (
    <>
      <h1>Xác thực hai lớp</h1>
      <p className="auth-lead">
        {useRecovery ? "Nhập một mã khôi phục bạn đã lưu khi bật xác thực hai lớp. Mỗi mã chỉ dùng được một lần." : "Mở ứng dụng xác thực trên điện thoại và nhập mã 6 chữ số đang hiển thị."}
      </p>
      <FormAlert message={formError} />
      {useRecovery ? <RecoveryCodeForm pending={verify.isPending} onSubmit={submit} /> : <TotpCodeForm pending={verify.isPending} onSubmit={submit} />}
      <p className="auth-links">
        <Button
          variant="line"
          size="sm"
          onClick={() => {
            setFormError(null);
            setUseRecovery((v) => !v);
          }}
        >
          {useRecovery ? "Dùng mã từ ứng dụng xác thực" : "Dùng mã khôi phục"}
        </Button>
        <Link to={loginPath}>Đăng nhập lại</Link>
      </p>
    </>
  );
}
