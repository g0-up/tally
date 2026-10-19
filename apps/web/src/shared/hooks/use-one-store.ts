import { createContext, useContext } from "react";

// Release 1.1 runs one store with one warehouse (ORG-03), so warehouse and branch columns and
// fields are hidden everywhere. The flag stays a context value so a later multi-store release
// flips it in one place.

export const OneStoreContext = createContext(true);

export function useOneStore(): boolean {
  return useContext(OneStoreContext);
}
