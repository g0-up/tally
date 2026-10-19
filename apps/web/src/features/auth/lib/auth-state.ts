// Router state handed between the auth screens. History state can hold anything (another tab,
// an old entry), so it is read through these guards, never cast.

/** /login may show a notice left by the screen that sent the visitor there. */
export interface LoginState {
  notice: string;
  /** "warn" when the visitor has to redo something (expired sign-in step). */
  tone?: NoticeTone;
}

export type NoticeTone = "ok" | "warn";

export interface Notice {
  message: string;
  tone: NoticeTone;
}

/** /mfa answers the challenge the password step returned, then continues to `next`. */
export interface MfaState {
  challenge: string;
  next: string | null;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null;
}

export function readLoginNotice(state: unknown): Notice | null {
  if (!isRecord(state) || typeof state.notice !== "string" || state.notice === "") return null;
  return { message: state.notice, tone: state.tone === "warn" ? "warn" : "ok" };
}

export function readMfaState(state: unknown): MfaState | null {
  if (!isRecord(state) || typeof state.challenge !== "string" || state.challenge === "") return null;
  return { challenge: state.challenge, next: typeof state.next === "string" ? state.next : null };
}

export const NOTICES = {
  verified: "Email đã được xác minh. Đăng nhập để vào cửa hàng.",
  passwordReset: "Đã đặt mật khẩu mới. Đăng nhập bằng mật khẩu mới.",
  signedOut: "Đã đăng xuất.",
} as const;
