import { useCallback } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { LOGIN_PATH } from "../../../app/guards/login-redirect";
import { isSessionExpired } from "../../../shared/api/api-error";
import { useApiClient } from "../../../shared/api/api-context";
import {
  ADMIN_AUTH_PATHS,
  AUTH_PATHS,
  authResultSchema,
  csrfTokenSchema,
  signupResultSchema,
  type AdminLoginBody,
  type AuthResult,
  type EmailBody,
  type LoginBody,
  type MfaChallengeBody,
  type SignupBody,
  type TokenPasswordBody,
  type VerifyBody,
} from "./contract";
import { resetStoredExports } from "../../reports";
import { NOTICES, type LoginState } from "../lib/auth-state";

// Mutations of the auth screens. Every request is `silent`: a wrong password or an expired link is
// a form error shown by the screen, not something the app-wide error handler should react to.

const silent = { silent: true } as const;

/**
 * The session (or its absence) just changed on this host: keep the CSRF token the API issued for
 * it and drop every cached answer of the previous identity, with the exports the tab was following,
 * so whoever signs in next on a shared till neither sees nor downloads them.
 */
export function useSessionChanged() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useCallback(
    (csrfToken: string | undefined) => {
      // Without a token in the body the CSRF cookie the API just set is used.
      client.setCsrfToken(csrfToken ?? null);
      queryClient.clear();
      resetStoredExports();
    },
    [client, queryClient],
  );
}

function useAuthStep<TBody>(path: string) {
  const client = useApiClient();
  const sessionChanged = useSessionChanged();
  return useMutation<AuthResult, unknown, TBody>({
    mutationFn: (body) => client.post(path, body, { ...silent, schema: authResultSchema }),
    onSuccess: (result) => {
      if (result.status === "signed_in") sessionChanged(result.csrf_token);
    },
  });
}

/** POST /auth/login: signed in, or an MFA challenge to answer on /mfa. */
export function useLogin() {
  return useAuthStep<LoginBody>(AUTH_PATHS.login);
}

/** POST /auth/mfa/totp/verify with the login challenge and a TOTP or recovery code. */
export function useVerifyMfa() {
  return useAuthStep<MfaChallengeBody>(AUTH_PATHS.mfaVerify);
}

/** Admin host: password step of the platform admin sign-in. */
export function useAdminLogin() {
  return useAuthStep<AdminLoginBody>(ADMIN_AUTH_PATHS.login);
}

/** Admin host: TOTP step of the platform admin sign-in. */
export function useAdminTotp() {
  return useAuthStep<{ challenge: string; code: string }>(ADMIN_AUTH_PATHS.totp);
}

/**
 * POST /auth/logout, then /login with "Đã đăng xuất.". The signed-in screens are left before the
 * cached data of the session is dropped, so nothing re-asks the API on the way out. A session that
 * had already ended (401) ends the same way.
 */
export function useLogout() {
  const client = useApiClient();
  const sessionChanged = useSessionChanged();
  const navigate = useNavigate();
  const leave = useCallback(
    async (csrfToken: string | undefined) => {
      const state: LoginState = { notice: NOTICES.signedOut };
      await navigate(LOGIN_PATH, { replace: true, state });
      sessionChanged(csrfToken);
    },
    [navigate, sessionChanged],
  );
  return useMutation<{ csrf_token: string }, unknown, undefined>({
    mutationFn: () => client.post(AUTH_PATHS.logout, undefined, { ...silent, schema: csrfTokenSchema }),
    onSuccess: (res) => leave(res.csrf_token),
    onError: async (e) => {
      if (isSessionExpired(e)) await leave(undefined);
    },
  });
}

/** POST /auth/signup on the root host: creates the store and mails the verification link. */
export function useSignup() {
  const client = useApiClient();
  return useMutation({
    mutationFn: (body: SignupBody) => client.post(AUTH_PATHS.signup, body, { ...silent, schema: signupResultSchema }),
  });
}

/** POST /store-lookup on the root host: mails the store addresses of an e-mail, if any. */
export function useStoreLookup() {
  const client = useApiClient();
  return useMutation({
    mutationFn: async (body: EmailBody) => {
      await client.post(AUTH_PATHS.storeLookup, body, silent);
    },
  });
}

/** POST /auth/verify: confirms the e-mail with the link token (and sets a first password). */
export function useVerifyEmail() {
  const client = useApiClient();
  return useMutation({
    mutationFn: async (body: VerifyBody) => {
      await client.post(AUTH_PATHS.verify, body, silent);
    },
  });
}

/** POST /auth/password-reset: mails a reset link when the e-mail has an account here. */
export function useRequestPasswordReset() {
  const client = useApiClient();
  return useMutation({
    mutationFn: async (body: EmailBody) => {
      await client.post(AUTH_PATHS.passwordReset, body, silent);
    },
  });
}

/** POST /auth/password-reset/confirm: sets the new password and signs out every session. */
export function useResetPassword() {
  const client = useApiClient();
  const sessionChanged = useSessionChanged();
  return useMutation({
    mutationFn: (body: TokenPasswordBody) => client.post(AUTH_PATHS.passwordResetConfirm, body, { ...silent, schema: csrfTokenSchema }),
    onSuccess: (res) => sessionChanged(res.csrf_token),
  });
}
