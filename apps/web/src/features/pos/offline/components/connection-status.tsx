import { Callout } from "../../../../shared/ui/callout";
import type { OfflineBundle, OutboxEntry } from "../offline-db";
import { foreignSalesMessage } from "../use-offline-sync";
import { OfflineBanner } from "./offline-banner";
import { SyncQueueCallout } from "./sync-queue-callout";

export interface ConnectionStatusProps {
  offline: boolean;
  bundle: OfflineBundle | null;
  /** Offline sales on this device still waiting to sync. */
  pending: readonly OutboxEntry[];
  syncing: boolean;
  onSync: () => void;
  /** Offline sales of another store left on this device. */
  foreign: number;
}

/**
 * The counter's connection notices: offline rules, sales waiting to sync, another store's sales.
 * They sit in one polite live region that is always on the page, so a reader hears the banner
 * when the network drops; a region inserted together with its text is often not read at all.
 */
export function ConnectionStatus({ offline, bundle, pending, syncing, onSync, foreign }: ConnectionStatusProps) {
  return (
    <div className="pos-status" aria-live="polite">
      {offline ? <OfflineBanner bundle={bundle} pending={pending} /> : <SyncQueueCallout count={pending.length} syncing={syncing} onSync={onSync} />}
      {foreign > 0 ? <Callout style={{ marginBottom: 16 }}>{foreignSalesMessage(foreign)}</Callout> : null}
    </div>
  );
}
