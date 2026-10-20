import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cx } from "../../lib/cx";

export interface TableWrapProps {
  /** Borderless variant inside cards and dialogs (`.tw.flat`). */
  flat?: boolean;
  /** Id of the element that names the table, normally its caption. */
  labelledBy: string;
  /** A single `<table>` that stays mounted; the wrapper watches it for width changes. */
  children: ReactNode;
}

/**
 * Prototype `.tw` wrapper. While its table is wider than the wrapper, the wrapper is a named region
 * that takes keyboard focus, so the hidden columns can be scrolled into view without a mouse
 * (WCAG 2.1.1). A table that fits adds no tab stop.
 */
export function TableWrap({ flat = false, labelledBy, children }: TableWrapProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [scrolls, setScrolls] = useState(false);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setScrolls(el.scrollWidth > el.clientWidth);
    measure();
    // The table grows as rows arrive without the wrapper changing size, so both are watched.
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    if (el.firstElementChild) observer.observe(el.firstElementChild);
    return () => observer.disconnect();
  }, []);

  return (
    <div
      ref={ref}
      className={cx("tw", flat && "flat")}
      {...(scrolls ? { tabIndex: 0, role: "region", "aria-labelledby": labelledBy } : {})}
    >
      {children}
    </div>
  );
}
