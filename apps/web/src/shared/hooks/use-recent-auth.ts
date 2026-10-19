import { createContext, useContext } from "react";

// Sensitive actions (write-off, period reopen, export, ownership transfer, MFA reset) need a
// password entry from the last few minutes. When the API answers `reauth_required`, the caller
// asks for re-authentication and retries the same intent once it succeeds.
//
// `useRecentAuth()` opens the shared ReauthDialog and resolves true when the API accepted the
// password (and TOTP code when MFA is on), false when the owner cancelled. The provider lives in
// `shared/ui/reauth-dialog.tsx` and is mounted once by the app.

export type RequestReauth = () => Promise<boolean>;

export const RecentAuthContext = createContext<RequestReauth | null>(null);

export function useRecentAuth(): RequestReauth {
  const request = useContext(RecentAuthContext);
  if (!request) throw new Error("useRecentAuth must be used inside ReauthProvider");
  return request;
}
