import type { ReactNode } from "react";
import { useRouteMeta } from "../hooks/use-route-meta";

export interface PageHeaderProps {
  /** Defaults to the menu group of the current route. */
  eyebrow?: string;
  /** Defaults to the menu title of the current route. */
  title?: string;
  lead?: ReactNode;
  actions?: ReactNode;
}

/** Prototype `pageHead()`: `.ph` with eyebrow, h1, lead and the action buttons on the right. */
export function PageHeader({ eyebrow, title, lead, actions }: PageHeaderProps) {
  const meta = useRouteMeta();
  return (
    <div className="ph">
      <div>
        <p className="eyebrow">{eyebrow ?? meta?.eyebrow ?? ""}</p>
        <h1>{title ?? meta?.title ?? ""}</h1>
        {lead !== undefined && lead !== null && lead !== "" ? <p className="lead">{lead}</p> : null}
      </div>
      <div className="acts">{actions}</div>
    </div>
  );
}
