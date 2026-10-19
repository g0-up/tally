import { useState, type ReactNode } from "react";
import { useRecentAuth } from "../../shared/hooks/use-recent-auth";
import { Button } from "../../shared/ui/button";
import { Callout } from "../../shared/ui/callout";

export const RECENT_AUTH_TEXT = "Phần này cần xác nhận lại mật khẩu trước khi xem hoặc thay đổi.";

/**
 * Wraps a sensitive section (ownership transfer, MFA reset, data export…): its content renders
 * only after the owner re-entered the password in this visit. Writes inside still go through
 * useCommit, which asks again if the API's five-minute window has passed.
 */
export function RequireRecentAuth({ children, text = RECENT_AUTH_TEXT }: { children: ReactNode; text?: string }) {
  const requestReauth = useRecentAuth();
  const [confirmed, setConfirmed] = useState(false);
  if (confirmed) return <>{children}</>;
  return (
    <Callout tone="info">
      {text}{" "}
      <Button size="sm" variant="navy" onClick={() => void requestReauth().then(setConfirmed)}>
        Xác nhận mật khẩu
      </Button>
    </Callout>
  );
}
