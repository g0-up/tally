import { useEffect } from "react";
import { useNavigate } from "react-router";
import { registerDocOpener } from "../../shared/domain/doc-no";

/** Screen each inventory document number opens on; the screen reads `?doc=`. */
const DOC_SCREENS: Readonly<Record<string, string>> = {
  DC: "/inventory/adjustments",
  KK: "/inventory/counts",
};

/** Registers the DC and KK openers so any DocNo link leads to its document. Call once in the shell. */
export function useInventoryDocOpeners(): void {
  const navigate = useNavigate();
  useEffect(() => {
    const unregister = Object.entries(DOC_SCREENS).map(([prefix, path]) =>
      registerDocOpener(prefix, (docNo) => void navigate(`${path}?doc=${encodeURIComponent(docNo)}`)),
    );
    return () => unregister.forEach((off) => off());
  }, [navigate]);
}
