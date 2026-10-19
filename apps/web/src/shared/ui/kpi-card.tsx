import type { ReactNode } from "react";
import { Link } from "react-router";
import { cx } from "../lib/cx";

export interface KpiCardProps {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  /** Dashed blue-50 card for projected figures. */
  forecast?: boolean;
  /** Makes the whole card a link to the source documents. */
  to?: string;
  onClick?: () => void;
}

/** Prototype `.kpi` (k / v / s lines); clickable cards are `.kpi.click`. */
export function KpiCard({ label, value, sub, forecast = false, to, onClick }: KpiCardProps) {
  const inner = (
    <>
      <span className="k">{label}</span>
      <span className="v">{value}</span>
      {sub !== undefined ? <span className="s">{sub}</span> : null}
    </>
  );
  const cls = cx("kpi", (to !== undefined || onClick) && "click", forecast && "forecast");
  if (to !== undefined) {
    return (
      <Link className={cls} to={to}>
        {inner}
      </Link>
    );
  }
  if (onClick) {
    return (
      <button type="button" className={cls} onClick={onClick}>
        {inner}
      </button>
    );
  }
  return <div className={cls}>{inner}</div>;
}
