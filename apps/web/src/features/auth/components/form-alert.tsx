import { Callout } from "../../../shared/ui/callout";
import type { Notice } from "../lib/auth-state";

/** Form-level error of an auth screen, announced by screen readers as soon as it appears. */
export function FormAlert({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <Callout tone="bad" role="alert">
      {message}
    </Callout>
  );
}

/** Information left by the previous step ("Đã đăng xuất.", "Email đã được xác minh…"). */
export function FormNotice({ notice }: { notice: Notice | null }) {
  if (!notice) return null;
  return (
    <Callout tone={notice.tone} role="status">
      {notice.message}
    </Callout>
  );
}
