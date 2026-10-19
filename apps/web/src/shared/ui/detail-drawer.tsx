import { useLayoutEffect, useRef, type ReactNode } from "react";
import { Dialog } from "radix-ui";
import { useEscapeLayer } from "../hooks/use-hotkeys";
import { useReturnFocus } from "../hooks/use-return-focus";
import { Callout } from "./callout";

export interface DetailDrawerProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  sub?: ReactNode;
  /** Header actions next to "×" (status-dependent buttons). */
  actions?: ReactNode;
  children: ReactNode;
  /** Key of the document shown; scroll resets only when it changes, not on data refresh. */
  docKey?: string;
  /** Where focus goes on close when the element that opened the drawer is gone. */
  returnFocusFallback?: () => HTMLElement | null | undefined;
}

/**
 * Prototype `openDrawer()`: right sheet `min(820px, 100%)` over a light scrim, sticky header with
 * title, sub line, actions and "×". Re-rendering after a refetch keeps the scroll position.
 */
export function DetailDrawer({ open, onClose, title, sub, actions, children, docKey, returnFocusFallback }: DetailDrawerProps) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  useEscapeLayer(open, onClose);
  const returnFocus = useReturnFocus(returnFocusFallback);

  useLayoutEffect(() => {
    if (bodyRef.current) bodyRef.current.scrollTop = 0;
  }, [docKey]);

  return (
    <Dialog.Root open={open} onOpenChange={(next) => (next ? undefined : onClose())}>
      <Dialog.Portal>
        <Dialog.Overlay className="drw-scrim" />
        <Dialog.Content
          ref={contentRef}
          className="drw"
          {...(sub === undefined ? { "aria-describedby": undefined } : {})}
          onEscapeKeyDown={(e) => e.preventDefault()}
          onCloseAutoFocus={returnFocus.onCloseAutoFocus}
          onOpenAutoFocus={(e) => {
            returnFocus.remember();
            // Focus the dialog itself so the reader starts at the title, not the first action.
            e.preventDefault();
            contentRef.current?.focus();
          }}
          tabIndex={-1}
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
            <div className="btn-row">
              {actions}
              <button className="x" type="button" aria-label="Đóng" onClick={onClose}>
                ×
              </button>
            </div>
          </div>
          <div className="drw-b" ref={bodyRef}>
            {children}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/** Body shown when the drawer content failed to load or render (prototype "Lỗi" drawer). */
export function DrawerError({ message }: { message: string }) {
  return <Callout tone="bad">{message}</Callout>;
}
