import { useSyncExternalStore } from "react";
import { dismissToast, getToasts, subscribeToasts, type ToastItem } from "./toast-store";

export const TOAST_DISMISS_LABEL = "Đóng thông báo";

/**
 * Renders the toast stack bottom-right (prototype `#toasts`). Mount once per app. Live regions are
 * never nested: success toasts go into a polite live region that is always on the page (so the
 * reader announces what is added to it); each error toast is its own `role="alert"`, announced as
 * it appears and kept until dismissed. Error toasts sit directly in the stack and spell out their
 * `aria-live`: an open modal dialog hides the rest of the page from readers except live regions
 * and their ancestors, and a refused write is reported while its dialog stays open.
 */
export function Toaster() {
  const items = useSyncExternalStore(subscribeToasts, getToasts, getToasts);
  const errors = items.filter((t) => t.kind === "err");
  const notices = items.filter((t) => t.kind !== "err");
  return (
    <div className="toasts">
      {errors.map((t) => (
        <ToastView key={t.id} item={t} />
      ))}
      <div className="toast-region" aria-live="polite">
        {notices.map((t) => (
          <ToastView key={t.id} item={t} />
        ))}
      </div>
    </div>
  );
}

function ToastView({ item }: { item: ToastItem }) {
  const err = item.kind === "err";
  return (
    <div className={err ? "toast err" : "toast"} role={err ? "alert" : undefined} aria-live={err ? "assertive" : undefined}>
      <div className="toast-msg">{item.message}</div>
      {err ? (
        <button type="button" className="toast-x" aria-label={TOAST_DISMISS_LABEL} onClick={() => dismissToast(item.id)}>
          ×
        </button>
      ) : null}
    </div>
  );
}
