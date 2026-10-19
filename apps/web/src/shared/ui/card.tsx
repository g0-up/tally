import type { HTMLAttributes, ReactNode } from "react";
import { cx } from "../lib/cx";

export interface CardProps extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  /** Card heading (h2) rendered in `.card-h`. */
  title?: ReactNode;
  /** Small note on the right of the heading. */
  note?: ReactNode;
  /** Actions placed after the note in the heading row. */
  actions?: ReactNode;
}

/** Prototype `.card` with optional `.card-h` heading row. */
export function Card({ title, note, actions, className, children, ...rest }: CardProps) {
  return (
    <div className={cx("card", className)} {...rest}>
      {title !== undefined || note !== undefined || actions !== undefined ? (
        <div className="card-h">
          {title !== undefined ? <h2>{title}</h2> : null}
          {note !== undefined ? <small>{note}</small> : null}
          {actions}
        </div>
      ) : null}
      {children}
    </div>
  );
}
