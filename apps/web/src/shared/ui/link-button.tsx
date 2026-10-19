import type { ButtonHTMLAttributes } from "react";
import { Link, type LinkProps } from "react-router";
import { cx } from "../lib/cx";

/** Prototype `.lnk`: a text-styled action. Renders a router link when `to` is given. */
export function LinkButton({ className, type = "button", ...rest }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button type={type} className={cx("lnk", className)} {...rest} />;
}

export function TextLink({ className, ...rest }: LinkProps) {
  return <Link className={cx("lnk", className)} {...rest} />;
}
