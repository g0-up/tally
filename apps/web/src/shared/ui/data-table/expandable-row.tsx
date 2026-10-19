import type { ReactNode } from "react";

export interface ExpandToggleProps {
  /** Name of the row's item, read in the accessible label. */
  name: string;
  expanded: boolean;
  onToggle: () => void;
}

/**
 * Prototype `.tgl`: 24×24 ▸/▾ toggle. The `.tgl-hit` wrapper grows to a 44px touch target on
 * coarse pointers without changing the visual size.
 */
export function ExpandToggle({ name, expanded, onToggle }: ExpandToggleProps) {
  return (
    <span className="tgl-hit">
      <button
        type="button"
        className="tgl"
        aria-expanded={expanded}
        aria-label={`${expanded ? "Thu gọn" : "Xem chi tiết"} màu của ${name}`}
        onClick={onToggle}
      >
        {expanded ? "▾" : "▸"}
      </button>
    </span>
  );
}

/** Spacer keeping names aligned on rows without a toggle. */
export function ExpandSpacer() {
  return <span className="tgl-sp" />;
}

/** Name cell of an expandable row: toggle (or spacer) followed by the content (`.tname`). */
export function ExpandableName({ toggle, children }: { toggle: ExpandToggleProps | null; children: ReactNode }) {
  return (
    <div className="tname">
      {toggle ? <ExpandToggle {...toggle} /> : <ExpandSpacer />}
      <div>{children}</div>
    </div>
  );
}

/** Indented sub row content (`.subrow`), e.g. one color of an expanded product. */
export function SubRow({ children, highlight = false }: { children: ReactNode; highlight?: boolean }) {
  return <div className={highlight ? "subrow subrow-hl" : "subrow"}>{children}</div>;
}
