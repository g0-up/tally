import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { Dialog } from "radix-ui";
import { useEscapeLayer } from "../hooks/use-hotkeys";
import { useReturnFocus } from "../hooks/use-return-focus";
import { Button, PENDING_LABEL, type ButtonVariant } from "./button";
import { Callout } from "./callout";

const AUTOFOCUS_SELECTOR = "[autofocus]:not(.cbx-in)";
const FIRST_FIELD_SELECTOR = "input:not(.cbx-in):not([type=checkbox]):not([type=radio]):not([type=hidden]):not(:disabled), select:not(:disabled), textarea:not(:disabled)";

export interface CommitDialogProps {
  open: boolean;
  /** Called on "Hủy", "×", Esc and a click on the scrim. */
  onClose: () => void;
  /** Where focus goes on close when the element that opened the dialog is gone (a deleted row). */
  returnFocusFallback?: () => HTMLElement | null | undefined;
  title: ReactNode;
  sub?: ReactNode;
  /** 1040px instead of 760px (document editors with line tables). */
  wide?: boolean;
  /** Form fields. */
  children: ReactNode;
  /**
   * Derived summary (totals, impact) re-rendered from the form state; it sits next to the
   * fields so typing never remounts them, inside a polite live region so changes are announced.
   */
  live?: ReactNode;
  /** Extra footer buttons between "Hủy" and the submit button. */
  extra?: ReactNode;
  /** Omit for read-only dialogs. */
  onSubmit?: () => void;
  submitLabel?: string;
  submitVariant?: ButtonVariant;
  submitDisabled?: boolean;
  /** A write is in flight: submit shows `pendingLabel` and the dialog cannot be dismissed. */
  pending?: boolean;
  /** Submit's label while pending, for writes that are not a posting ("Đang gửi…"). */
  pendingLabel?: string;
  cancelLabel?: string;
  /**
   * Asked before closing, e.g. while a write may have been stored without an answer: dismissing
   * then shows the question in the footer, and only "Vẫn đóng" calls `onClose`.
   */
  closeWarning?: string;
}

/**
 * Prototype `openModal()` on a Radix dialog: scrim `.ovl`, `.modal(.wide)` with header, form body,
 * optional live region and footer. Focus goes to the first non-combobox field and returns to the
 * opener on close; Esc closes only the topmost layer.
 */
export function CommitDialog({
  open,
  onClose,
  title,
  sub,
  wide = false,
  children,
  live,
  extra,
  onSubmit,
  submitLabel = "Ghi sổ",
  submitVariant = "primary",
  submitDisabled = false,
  pending = false,
  pendingLabel = PENDING_LABEL,
  cancelLabel = "Hủy",
  closeWarning,
  returnFocusFallback,
}: CommitDialogProps) {
  const contentRef = useRef<HTMLDivElement>(null);
  const [askedToClose, setAskedToClose] = useState(false);
  const asking = askedToClose && closeWarning !== undefined && !pending;
  const close = () => {
    setAskedToClose(false);
    onClose();
  };
  const dismiss = () => {
    if (pending) return;
    // Esc or the scrim while the question is up answer it with "stay".
    if (asking) setAskedToClose(false);
    else if (closeWarning !== undefined) setAskedToClose(true);
    else close();
  };
  useEscapeLayer(open, dismiss);
  const returnFocus = useReturnFocus(returnFocusFallback);

  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    if (!pending && !submitDisabled) onSubmit?.();
  };

  return (
    <Dialog.Root open={open} onOpenChange={(next) => (next ? undefined : dismiss())}>
      <Dialog.Portal>
        <Dialog.Overlay className="ovl">
          <Dialog.Content
            ref={contentRef}
            className={wide ? "modal wide" : "modal"}
            {...(sub === undefined ? { "aria-describedby": undefined } : {})}
            onEscapeKeyDown={(e) => e.preventDefault()}
            onCloseAutoFocus={returnFocus.onCloseAutoFocus}
            onOpenAutoFocus={(e) => {
              returnFocus.remember();
              const root = contentRef.current;
              const target = root?.querySelector<HTMLElement>(AUTOFOCUS_SELECTOR) ?? root?.querySelector<HTMLElement>(FIRST_FIELD_SELECTOR);
              if (target) {
                e.preventDefault();
                target.focus();
              }
            }}
          >
            <div className="m-h">
              <div>
                <Dialog.Title asChild>
                  <h3>{title}</h3>
                </Dialog.Title>
                {sub !== undefined ? (
                  <Dialog.Description asChild>
                    <p>{sub}</p>
                  </Dialog.Description>
                ) : null}
              </div>
              <button className="x" type="button" aria-label="Đóng" onClick={dismiss}>
                ×
              </button>
            </div>
            <form className="m-b" noValidate onSubmit={submit}>
              {children}
              {/* Always on the page so changes to it (impact, limit warnings) are announced. */}
              <div className="m-live" aria-live="polite">
                {live}
              </div>
            </form>
            {asking ? (
              <div className="m-f">
                <Callout tone="warn" role="alert" style={{ flex: "1 1 100%" }}>
                  {closeWarning}
                </Callout>
                <Button variant="line" onClick={() => setAskedToClose(false)} autoFocus>
                  Ở lại
                </Button>
                <Button variant="danger" onClick={close}>
                  Vẫn đóng
                </Button>
              </div>
            ) : (
              <div className="m-f">
                <Button variant="line" onClick={dismiss} disabled={pending}>
                  {cancelLabel}
                </Button>
                {extra}
                {onSubmit ? (
                  <Button variant={submitVariant} onClick={() => submit()} pending={pending} pendingLabel={pendingLabel} disabled={submitDisabled}>
                    {submitLabel}
                  </Button>
                ) : null}
              </div>
            )}
          </Dialog.Content>
        </Dialog.Overlay>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
