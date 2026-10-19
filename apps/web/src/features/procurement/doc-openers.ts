import { useEffect } from "react";
import { useNavigate } from "react-router";
import { registerDocOpener } from "../../shared/domain/doc-no";

/** Screen each procurement document number opens on; the screen reads `?doc=`. */
const DOC_SCREENS: Readonly<Record<string, string>> = {
  PO: "/purchasing/orders",
  PN: "/purchasing/receipts",
  XT: "/purchasing/supplier-returns",
};

/** Registers the PO, PN and XT openers so any DocNo link leads to its document. Call once in the shell. */
export function useProcurementDocOpeners(): void {
  const navigate = useNavigate();
  useEffect(() => {
    const unregister = Object.entries(DOC_SCREENS).map(([prefix, path]) =>
      registerDocOpener(prefix, (docNo) => void navigate(`${path}?doc=${encodeURIComponent(docNo)}`)),
    );
    return () => unregister.forEach((off) => off());
  }, [navigate]);
}
