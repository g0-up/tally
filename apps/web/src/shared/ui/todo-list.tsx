import type { ReactNode } from "react";
import { Link } from "react-router";

export interface TodoItem {
  key: string;
  /** Leading cell, usually a StatusPill. */
  lead?: ReactNode;
  title: ReactNode;
  sub?: ReactNode;
  /** Trailing figure (`.n`): a count or a document number. */
  count?: ReactNode;
  /** Route to open; otherwise the row is a button calling onClick. */
  to?: string;
  onClick?: () => void;
}

/** Prototype `.todo`: rows of lead / text with small sub-line / trailing number, each one actionable. */
export function TodoList({ items }: { items: readonly TodoItem[] }) {
  return (
    <div className="todo">
      {items.map((it) => {
        const body = (
          <>
            {it.lead ?? <span />}
            <span>
              {it.title}
              {it.sub !== undefined ? <small>{it.sub}</small> : null}
            </span>
            <span className="n">{it.count}</span>
          </>
        );
        return it.to !== undefined ? (
          <Link key={it.key} to={it.to}>
            {body}
          </Link>
        ) : (
          <button key={it.key} type="button" onClick={it.onClick}>
            {body}
          </button>
        );
      })}
    </div>
  );
}
