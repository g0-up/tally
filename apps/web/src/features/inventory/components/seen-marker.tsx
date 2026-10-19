import { useCallback } from "react";

export interface SeenMarkerProps {
  id: string;
  /** Called once the marker comes near the viewport; must keep its identity across renders. */
  onSeen: (id: string) => void;
}

/** An empty mark inside a row that reports the row as shown; without IntersectionObserver it reports at once. */
export function SeenMarker({ id, onSeen }: SeenMarkerProps) {
  const ref = useCallback(
    (el: HTMLSpanElement | null) => {
      if (el === null) return;
      if (typeof IntersectionObserver === "undefined") {
        onSeen(id);
        return;
      }
      const io = new IntersectionObserver(
        (entries) => {
          if (!entries.some((e) => e.isIntersecting)) return;
          io.disconnect();
          onSeen(id);
        },
        { rootMargin: "200px" },
      );
      io.observe(el);
      return () => io.disconnect();
    },
    [id, onSeen],
  );
  return <span ref={ref} aria-hidden="true" />;
}
