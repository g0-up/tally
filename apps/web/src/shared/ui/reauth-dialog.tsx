import { useCallback, useRef, useState, type ReactNode } from "react";
import { useApiClient } from "../api/api-context";
import { errorMessage, isApiError } from "../api/api-error";
import { PATHS } from "../api/endpoints";
import { RecentAuthContext, type ReauthCopy, type RequestReauth } from "../hooks/use-recent-auth";
import { CommitDialog } from "./commit-dialog";
import { Field } from "./field";
import { Input } from "./input";

export const REAUTH_TITLE = "Xác nhận lại mật khẩu";
export const REAUTH_SUB = "Thao tác này cần xác nhận lại danh tính. Nhập mật khẩu đăng nhập để tiếp tục.";
export const REAUTH_SUBMIT = "Xác nhận";
const PASSWORD_REQUIRED = "Nhập mật khẩu.";
const WRONG_PASSWORD = "Mật khẩu không đúng.";
/** API code for a wrong password on POST /auth/reauth (403). */
const INVALID_PASSWORD = "invalid_password";

export interface ReauthDialogProps {
  open: boolean;
  onClose: () => void;
  /** Resolves when the API accepted the credentials; rejects with the API error otherwise. */
  onSubmit: (password: string) => Promise<void>;
  /** Wording of the prompt; the shared wording by default. */
  copy?: ReauthCopy | null;
}

/** Password prompt for actions that need a recent sign-in. The password never leaves component state. */
export function ReauthDialog({ open, onClose, onSubmit, copy }: ReauthDialogProps) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const close = () => {
    setPassword("");
    setError(null);
    onClose();
  };

  const submit = async () => {
    if (password === "") {
      setError(PASSWORD_REQUIRED);
      return;
    }
    setPending(true);
    setError(null);
    try {
      await onSubmit(password);
      setPassword("");
    } catch (e) {
      setError(isApiError(e) && (e.code === INVALID_PASSWORD || e.status === 422) ? WRONG_PASSWORD : errorMessage(e));
    } finally {
      setPending(false);
    }
  };

  return (
    <CommitDialog open={open} onClose={close} title={copy?.title ?? REAUTH_TITLE} sub={copy?.sub ?? REAUTH_SUB} submitLabel={copy?.submitLabel ?? REAUTH_SUBMIT} pending={pending} onSubmit={() => void submit()}>
      <div className="fg">
        <Field label="Mật khẩu" error={error ?? undefined}>
          <Input type="password" autoComplete="current-password" value={password} invalid={error !== null} onChange={(e) => setPassword(e.target.value)} />
        </Field>
      </div>
    </CommitDialog>
  );
}

/**
 * Mounts the shared re-authentication dialog. Concurrent requests share one prompt; the promise
 * resolves true after `POST /auth/reauth` succeeds and false when the owner cancels.
 */
export function ReauthProvider({ children }: { children: ReactNode }) {
  const client = useApiClient();
  const [open, setOpen] = useState(false);
  const [copy, setCopy] = useState<ReauthCopy | null>(null);
  const waiting = useRef<{ promise: Promise<boolean>; resolve: (ok: boolean) => void } | null>(null);

  const settle = useCallback((ok: boolean) => {
    const w = waiting.current;
    waiting.current = null;
    setOpen(false);
    w?.resolve(ok);
  }, []);

  const request = useCallback<RequestReauth>((wording) => {
    if (waiting.current) return waiting.current.promise;
    setCopy(wording ?? null);
    let resolve: (ok: boolean) => void = () => undefined;
    const promise = new Promise<boolean>((r) => {
      resolve = r;
    });
    waiting.current = { promise, resolve };
    setOpen(true);
    return promise;
  }, []);

  const submit = async (password: string) => {
    // Silent: a wrong password must stay in the dialog instead of triggering the app-wide error handling.
    await client.post(PATHS.reauth, { password }, { silent: true });
    settle(true);
  };

  return (
    <RecentAuthContext.Provider value={request}>
      {children}
      <ReauthDialog open={open} onClose={() => settle(false)} onSubmit={submit} copy={copy} />
    </RecentAuthContext.Provider>
  );
}
