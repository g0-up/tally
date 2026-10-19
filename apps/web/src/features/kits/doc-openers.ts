import { useEffect } from "react";
import { useNavigate } from "react-router";
import { registerDocOpener } from "../../shared/domain/doc-no";

/** Screen each gift-basket document number opens on; the screen reads `?doc=`. */
const DOC_SCREENS: Readonly<Record<string, string>> = {
  LD: "/gift-baskets/assembly-orders",
  TG: "/gift-baskets/disassembly",
};

/** Registers the LD and TG openers so any DocNo link leads to its document. Call once in the shell. */
export function useKitsDocOpeners(): void {
  const navigate = useNavigate();
  useEffect(() => {
    const unregister = Object.entries(DOC_SCREENS).map(([prefix, path]) =>
      registerDocOpener(prefix, (docNo) => void navigate(`${path}?doc=${encodeURIComponent(docNo)}`)),
    );
    return () => unregister.forEach((off) => off());
  }, [navigate]);
}
