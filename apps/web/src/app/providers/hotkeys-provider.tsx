import { useEffect, type ReactNode } from "react";
import { installHotkeys } from "../../shared/hooks/use-hotkeys";

/** Installs the single document keydown listener behind the Esc chain and screen hotkeys. */
export function HotkeysProvider({ children }: { children: ReactNode }) {
  useEffect(() => installHotkeys(document), []);
  return <>{children}</>;
}
