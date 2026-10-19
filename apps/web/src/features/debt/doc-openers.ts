import { useEffect } from "react";
import { useNavigate } from "react-router";
import { registerDocOpener } from "../../shared/domain/doc-no";

/**
 * Screen each debt document number opens on; the screen reads `?doc=` and looks the document up
 * by number: PT and PC open the payment drawer, DN and BT the adjustment or offset drawer.
 */
const DOC_SCREENS: Readonly<Record<string, string>> = {
  PT: "/debts/advances",
  PC: "/debts/advances",
  DS: "/debts/statements",
  DN: "/debts/receivables",
  BT: "/debts/receivables",
};

/** Registers the PT, PC, DS, DN and BT openers so any DocNo link leads to its screen. Call once in the shell. */
export function useDebtDocOpeners(): void {
  const navigate = useNavigate();
  useEffect(() => {
    const unregister = Object.entries(DOC_SCREENS).map(([prefix, path]) =>
      registerDocOpener(prefix, (docNo) => void navigate(`${path}?doc=${encodeURIComponent(docNo)}`)),
    );
    return () => unregister.forEach((off) => off());
  }, [navigate]);
}
