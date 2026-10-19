import type { ReactNode } from "react";

export interface TimelineItem {
  key: string;
  time: ReactNode;
  content: ReactNode;
}

export const EMPTY_TIMELINE_TEXT = "Chưa có lịch sử.";

/** Prototype `.tl`: 92px time column then the event text; empty shows "Chưa có lịch sử.". */
export function Timeline({ items, empty = EMPTY_TIMELINE_TEXT }: { items: readonly TimelineItem[]; empty?: ReactNode }) {
  if (items.length === 0) return <p className="hint">{empty}</p>;
  return (
    <div className="tl">
      {items.map((it) => (
        <div key={it.key}>
          <time>{it.time}</time>
          <span>{it.content}</span>
        </div>
      ))}
    </div>
  );
}
