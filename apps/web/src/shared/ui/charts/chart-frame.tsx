import { useRef, useState, type ReactNode } from "react";

export interface ChartHit {
  key: string;
  x: number;
  y: number;
  width: number;
  height: number;
  /** Tooltip content; also the accessible name of the focusable bar. */
  tip: ReactNode;
  tipLabel: string;
}

export interface ChartFrameProps {
  width: number;
  height: number;
  ariaLabel: string;
  /** Grid, axes and bars. */
  children: ReactNode;
  hits: readonly ChartHit[];
  legend: ReadonlyArray<{ name: string; color: string }>;
  /** Accessible data table shown under "Xem dạng bảng". */
  table: ReactNode;
}

interface TipState {
  key: string;
  left: number;
  top: number;
}

/**
 * `.chart` wrapper: SVG, hover/focus tooltip, legend and the "Xem dạng bảng" disclosure with the
 * same numbers as a table. Each bar has a focusable hit area so keyboard users get the tooltip too.
 */
export function ChartFrame({ width, height, ariaLabel, children, hits, legend, table }: ChartFrameProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const [tip, setTip] = useState<TipState | null>(null);

  const showAt = (key: string, clientX: number, clientY: number) => {
    const box = boxRef.current?.getBoundingClientRect();
    if (!box) return;
    const tw = tipRef.current?.offsetWidth ?? 0;
    let left = clientX - box.left + 14;
    if (left + tw > box.width) left = clientX - box.left - tw - 14;
    setTip({ key, left: Math.max(0, left), top: Math.max(0, clientY - box.top - 20) });
  };

  const showForHit = (h: ChartHit, el: SVGRectElement) => {
    const r = el.getBoundingClientRect();
    showAt(h.key, r.left + r.width / 2, r.top + r.height / 3);
  };

  const active = tip ? hits.find((h) => h.key === tip.key) : undefined;

  return (
    <>
      <div className="chart" ref={boxRef} onMouseLeave={() => setTip(null)}>
        <svg viewBox={`0 0 ${width} ${height}`} role="group" aria-label={ariaLabel}>
          {children}
          {hits.map((h) => (
            <rect
              key={h.key}
              className="hit"
              x={h.x}
              y={h.y}
              width={h.width}
              height={h.height}
              tabIndex={0}
              role="img"
              aria-label={h.tipLabel}
              onMouseMove={(e) => showAt(h.key, e.clientX, e.clientY)}
              onFocus={(e) => showForHit(h, e.currentTarget)}
              onBlur={() => setTip(null)}
            />
          ))}
        </svg>
        <div className="tip" ref={tipRef} hidden={!active} style={tip ? { left: tip.left, top: tip.top } : undefined} aria-hidden="true">
          {active?.tip}
        </div>
      </div>
      <div className="legend">
        {legend.map((s) => (
          <span key={s.name}>
            <i style={{ background: s.color }} aria-hidden="true" />
            {s.name}
          </span>
        ))}
      </div>
      <details style={{ marginTop: 12 }}>
        <summary className="lnk">Xem dạng bảng</summary>
        {table}
      </details>
    </>
  );
}
