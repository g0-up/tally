import { NavLink } from "react-router";
import { useRouteMeta } from "../hooks/use-route-meta";

export interface TabItem {
  to: string;
  label: string;
}

/** Prototype `tabsHtml()`: `nav.tabs` of links, the current one with aria-current="page". */
export function Tabs({ items, label }: { items: readonly TabItem[]; label: string }) {
  return (
    <nav className="tabs" aria-label={label}>
      {items.map((t) => (
        <NavLink key={t.to} to={t.to} end>
          {t.label}
        </NavLink>
      ))}
    </nav>
  );
}

/** Tabs of the current menu group (sibling routes). */
export function ModuleTabs() {
  const meta = useRouteMeta();
  if (!meta) return null;
  return <Tabs items={meta.tabs} label={meta.groupLabel} />;
}
