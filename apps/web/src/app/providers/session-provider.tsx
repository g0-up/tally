import type { ReactNode } from "react";
import { ExceptionConfirmProvider } from "../../shared/ui/exception-confirm-dialog";
import { ReauthProvider } from "../../shared/ui/reauth-dialog";

/**
 * Session-wide prompts every write can raise: re-authentication (`reauth_required`) and the
 * owner's exception confirmation (409 + exception_kind). Mounted once inside the router so the
 * dialogs can use links and the API client.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
  return (
    <ReauthProvider>
      <ExceptionConfirmProvider>{children}</ExceptionConfirmProvider>
    </ReauthProvider>
  );
}
