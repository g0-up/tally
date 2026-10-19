import { useState, useSyncExternalStore } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { MENU, firstSubPath, groupOfPath, subPath } from "../menu";
import { BRAND_NAME, BRAND_TAGLINE, SANDBOX_FOOTNOTE } from "./brand";

export const SIDEBAR_ID = "side";

/** Below this width the sidebar is an off-canvas sheet (matches the stylesheet breakpoint). */
export const OFF_CANVAS_QUERY = "(max-width: 860px)";

const expandGroupLabel = (group: string) => `Mở rộng ${group}`;
const collapseGroupLabel = (group: string) => `Thu gọn ${group}`;

export interface SidebarProps {
  /** Off-canvas state below 860px. */
  open: boolean;
  onClose: () => void;
  sandbox: boolean;
}

function subscribeOffCanvas(onChange: () => void): () => void {
  const list = window.matchMedia(OFF_CANVAS_QUERY);
  list.addEventListener("change", onChange);
  return () => list.removeEventListener("change", onChange);
}

/** True while the sidebar is an off-canvas sheet. */
function useOffCanvas(): boolean {
  return useSyncExternalStore(subscribeOffCanvas, () => window.matchMedia(OFF_CANVAS_QUERY).matches, () => false);
}

/**
 * Prototype sidebar: brand, menu groups and, for the sandbox store only, the demo footnote. Each
 * group name links to the group's first screen; a separate chevron folds or unfolds its links.
 * The current group starts unfolded; what the user folds or unfolds is kept for the current path.
 * Off-canvas and closed, the sheet is inert so keyboard and reader users cannot land in it.
 */
export function Sidebar({ open, onClose, sandbox }: SidebarProps) {
  const location = useLocation();
  const current = groupOfPath(location.pathname);
  const offCanvas = useOffCanvas();
  const [toggled, setToggled] = useState<{ at: string; open: Readonly<Record<string, boolean>> }>({ at: "", open: {} });
  const overrides = toggled.at === location.pathname ? toggled.open : {};

  return (
    <>
      <aside id={SIDEBAR_ID} className={open ? "side open" : "side"} aria-label="Điều hướng" inert={offCanvas && !open}>
        <div className="brand">
          <b>{BRAND_NAME}</b>
          <span>{BRAND_TAGLINE}</span>
        </div>
        <nav className="nav" aria-label="Menu chính">
          {MENU.map((g) => {
            const isCurrent = current?.id === g.id;
            const expanded = overrides[g.id] ?? isCurrent;
            const subId = `nav-sub-${g.id}`;
            return (
              <div key={g.id}>
                <div className="nav-gr" data-open={expanded || undefined}>
                  <Link className="nav-g" to={firstSubPath(g)} aria-current={isCurrent ? "true" : undefined} onClick={onClose}>
                    {g.label}
                  </Link>
                  <button
                    className="nav-tg"
                    type="button"
                    aria-expanded={expanded}
                    aria-controls={subId}
                    aria-label={expanded ? collapseGroupLabel(g.label) : expandGroupLabel(g.label)}
                    onClick={() => setToggled({ at: location.pathname, open: { ...overrides, [g.id]: !expanded } })}
                  >
                    <span aria-hidden="true">{expanded ? "▾" : "▸"}</span>
                  </button>
                </div>
                <div className="nav-sub" id={subId} hidden={!expanded}>
                  {g.subs.map((s) => (
                    <NavLink key={s.id} to={subPath(g, s)} end onClick={onClose}>
                      {s.label}
                    </NavLink>
                  ))}
                </div>
              </div>
            );
          })}
        </nav>
        {sandbox ? <div className="side-foot">{SANDBOX_FOOTNOTE}</div> : null}
      </aside>
      <div className="side-scrim" aria-hidden="true" onClick={onClose} />
    </>
  );
}
