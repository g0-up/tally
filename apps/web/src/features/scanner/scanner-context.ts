import { createContext, useContext } from "react";
import type { ScannerController } from "./scanner-controller";

/** The tab's phone scanner, or null outside a ScannerProvider (pairing cards then render nothing). */
export const ScannerContext = createContext<ScannerController | null>(null);

export function useScanner(): ScannerController | null {
  return useContext(ScannerContext);
}
