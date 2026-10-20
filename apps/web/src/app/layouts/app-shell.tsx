import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router";
import { useBusinessDate, useSession, useTenant, useUnreadCount } from "../../shared/api/platform-queries";
import { useDocumentTitle } from "../../shared/hooks/use-document-title";
import { useEscapeLayer } from "../../shared/hooks/use-hotkeys";
import { useReadOnly } from "../../shared/hooks/use-read-only";
import { useRouteTitle } from "../../shared/hooks/use-route-meta";
import { ReadOnlyBanner } from "../../shared/ui/read-only-banner";
import { useDebtDocOpeners } from "../../features/debt";
import { useInventoryDocOpeners } from "../../features/inventory";
import { useKitsDocOpeners } from "../../features/kits";
import { useProcurementDocOpeners } from "../../features/procurement";
import { ScannerProvider } from "../../features/scanner";
import { TenantProvider } from "../providers/tenant-provider";
import { SIDEBAR_ID, Sidebar } from "./sidebar";
import { UtilBar } from "./util-bar";

export const SKIP_LINK_LABEL = "Bỏ qua tới nội dung";

/** Store app shell: sidebar, util bar and the current screen, with store-wide context around them. */
export function AppShell() {
  return (
    <TenantProvider>
      <ScannerProvider>
        <ShellFrame />
      </ScannerProvider>
    </TenantProvider>
  );
}

function ShellFrame() {
  const location = useLocation();
  const session = useSession();
  const tenant = useTenant();
  const businessDate = useBusinessDate();
  const unread = useUnreadCount();
  const { readOnly } = useReadOnly();
  useProcurementDocOpeners();
  useInventoryDocOpeners();
  useKitsDocOpeners();
  useDebtDocOpeners();

  // The off-canvas menu belongs to the path it was opened on: navigating closes it.
  const [menuAt, setMenuAt] = useState<string | null>(null);
  const menuOpen = menuAt === location.pathname;
  const closeMenu = () => setMenuAt(null);
  useEscapeLayer(menuOpen, closeMenu);
  useDocumentTitle(useRouteTitle());

  // Opening the off-canvas menu moves focus into it; closing it while focus is inside (Esc, scrim)
  // hands focus back to the menu button so it never lands on the now-inert sheet.
  const wasOpen = useRef(false);
  useEffect(() => {
    const side = document.getElementById(SIDEBAR_ID);
    if (menuOpen && !wasOpen.current) side?.querySelector<HTMLElement>("a[href], button")?.focus();
    if (!menuOpen && wasOpen.current && side?.contains(document.activeElement)) {
      document.querySelector<HTMLElement>(`[aria-controls="${SIDEBAR_ID}"]`)?.focus();
    }
    wasOpen.current = menuOpen;
  }, [menuOpen]);

  // A new screen starts at the top, like the prototype's render().
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.pathname]);

  const sandbox = tenant.data?.is_sandbox ?? session.data?.tenant?.is_sandbox ?? false;
  return (
    <div className="app">
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
      <Sidebar open={menuOpen} onClose={closeMenu} sandbox={sandbox} />
      <div style={{ minWidth: 0 }}>
        <UtilBar
          menuOpen={menuOpen}
          onMenu={() => setMenuAt(menuOpen ? null : location.pathname)}
          sandbox={sandbox}
          tenantName={session.data?.tenant?.display_name}
          businessDate={businessDate}
          ownerName={session.data?.user.full_name}
          unread={unread.data?.count ?? null}
        />
        <main className="view" id="view" tabIndex={-1}>
          {readOnly ? <ReadOnlyBanner /> : null}
          <Outlet />
        </main>
      </div>
    </div>
  );
}
