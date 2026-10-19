// Changes to what the device stores for the offline POS, told to every tab of the store: a sale
// saved, synced or settled in one tab refreshes the outbox, the banner and the cap of the others,
// whose IndexedDB reads are otherwise cached until they change something themselves. The tab that
// made the change hears it too, through the same listeners.

export type OfflineEvent =
  /** The outbox or the unanswered checkouts of a tenant changed. */
  | { type: "changed"; tenantId: string }
  /** An unanswered online checkout got its answer: posted (or replayed), or refused for good. */
  | { type: "intent-settled"; tenantId: string; key: string; posted: boolean };

const CHANNEL = "tally-pos-offline";

type Listener = (event: OfflineEvent) => void;
const listeners = new Set<Listener>();
let channel: BroadcastChannel | null | undefined;

function isOfflineEvent(v: unknown): v is OfflineEvent {
  if (typeof v !== "object" || v === null) return false;
  const e = v as Record<string, unknown>;
  if (typeof e.tenantId !== "string") return false;
  if (e.type === "changed") return true;
  return e.type === "intent-settled" && typeof e.key === "string" && typeof e.posted === "boolean";
}

function deliver(event: OfflineEvent): void {
  for (const l of [...listeners]) l(event);
}

/** The page's channel, opened on first use; null where the browser has none. */
function openChannel(): BroadcastChannel | null {
  if (channel !== undefined) return channel;
  if (typeof BroadcastChannel === "undefined") {
    channel = null;
    return channel;
  }
  channel = new BroadcastChannel(CHANNEL);
  channel.onmessage = (m: MessageEvent<unknown>) => {
    if (isOfflineEvent(m.data)) deliver(m.data);
  };
  // Node's channel (unit tests) would otherwise keep the process alive; browsers have no unref.
  (channel as BroadcastChannel & { unref?: () => void }).unref?.();
  return channel;
}

/** Tells this tab and the store's other tabs. */
export function publishOfflineEvent(event: OfflineEvent): void {
  deliver(event);
  openChannel()?.postMessage(event);
}

export function subscribeOfflineEvents(listener: Listener): () => void {
  openChannel();
  listeners.add(listener);
  return () => listeners.delete(listener);
}
