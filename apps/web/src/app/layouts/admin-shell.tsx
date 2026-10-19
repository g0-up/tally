import type { CSSProperties } from "react";
import { NavLink, Outlet, useLocation } from "react-router";
import { useAdminLogout } from "../../features/admin";
import { errorMessage, isSessionExpired } from "../../shared/api/api-error";
import { useAdminSession } from "../../shared/api/platform-queries";
import { useDocumentTitle } from "../../shared/hooks/use-document-title";
import { toast } from "../../shared/ui/toast-store";
import { SKIP_LINK_LABEL } from "./app-shell";

export const ADMIN_BRAND = "Tally";
export const ADMIN_TAGLINE = "Quản trị nền tảng";
export const ADMIN_NAV_LABEL = "Quản trị nền tảng";
export const ADMIN_LOGOUT_LABEL = "Đăng xuất";

/** Console sections, in menu order. */
const ADMIN_NAV: ReadonlyArray<{ to: string; label: string }> = [
  { to: "/admin/tenants", label: "Cửa hàng" },
  { to: "/admin/usage", label: "Mức sử dụng" },
  { to: "/admin/invoices", label: "Hóa đơn thuê bao" },
  { to: "/admin/incidents", label: "Sự cố" },
  { to: "/admin/feature-flags", label: "Cờ tính năng" },
  { to: "/admin/support", label: "Hỗ trợ" },
  { to: "/admin/partners", label: "Đối tác giới thiệu" },
];

// The global focus ring is navy, invisible on the dark header: its colour is set here, its width
// and style still come from `:focus-visible`, so nothing shows until an element has focus.
const focusRing = "var(--on-dark-focus)";
// A dark header instead of the store's navy one, so the console is never mistaken for a store.
const headerStyle: CSSProperties = { background: "var(--surface-dark)", color: "var(--on-accent)", alignItems: "center" };
const navStyle: CSSProperties = { display: "flex", flexWrap: "wrap", gap: "var(--space-2) var(--space-4)", flex: "1 1 auto" };
const linkStyle = ({ isActive }: { isActive: boolean }): CSSProperties => ({
  color: "var(--on-accent)",
  textDecoration: isActive ? "underline" : "none",
  textUnderlineOffset: 4,
  fontWeight: isActive ? 700 : 400,
  outlineColor: focusRing,
});
// `.btn-sm` gives the 24px minimum and the 44px target on narrow and touch screens; the border
// reaches 3:1 against the dark header.
const logoutStyle: CSSProperties = { background: "transparent", color: "var(--on-accent)", border: "1px solid var(--on-dark-line)", borderRadius: "var(--radius-sm)", cursor: "pointer", outlineColor: focusRing };

function AdminLogoutButton() {
  const logout = useAdminLogout();
  return (
    <button
      type="button"
      className="btn-sm"
      style={logoutStyle}
      disabled={logout.isPending}
      aria-busy={logout.isPending || undefined}
      onClick={() =>
        logout.mutate(undefined, {
          onError: (e) => {
            if (!isSessionExpired(e)) toast.error(`Không đăng xuất được: ${errorMessage(e)}`);
          },
        })
      }
    >
      {ADMIN_LOGOUT_LABEL}
    </button>
  );
}

/** The signed-in admin, from the session the guard has already read. */
function AdminIdentity() {
  const admin = useAdminSession().data?.admin;
  if (!admin) return null;
  // The e-mail is in the hover title and, for screen readers, spoken after the name.
  return (
    <>
      <span title={admin.email} style={{ color: "var(--on-accent)" }}>
        {admin.full_name}
      </span>
      <span className="sr-only">{admin.email}</span>
    </>
  );
}

export function AdminShell() {
  const { pathname } = useLocation();
  const section = ADMIN_NAV.find((item) => pathname === item.to || pathname.startsWith(`${item.to}/`));
  useDocumentTitle(section ? `${section.label} · ${ADMIN_TAGLINE}` : ADMIN_TAGLINE);
  return (
    <div className="onb">
      <a
        className="skip-link"
        href="#view"
        onClick={(e) => {
          // Focus the main region directly instead of adding "#view" to the routed URL.
          e.preventDefault();
          document.getElementById("view")?.focus();
        }}
      >
        {SKIP_LINK_LABEL}
      </a>
      <header className="onb-top" style={headerStyle}>
        <b style={{ color: "var(--on-accent)" }}>{ADMIN_BRAND}</b>
        <span>{ADMIN_TAGLINE}</span>
        <nav aria-label={ADMIN_NAV_LABEL} style={navStyle}>
          {ADMIN_NAV.map((item) => (
            <NavLink key={item.to} to={item.to} style={linkStyle}>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <AdminIdentity />
        <AdminLogoutButton />
      </header>
      <main className="view" id="view" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}
