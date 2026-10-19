import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage } from "../../../shared/api/api-error";
import { platformKeys } from "../../../shared/api/query-keys";
import { vnd } from "../../../shared/lib/format";
import { toast } from "../../../shared/ui/toast-store";
import { POSTING_KEYS, withDoc } from "../../sales/api/mutations";
import { forgetCheckoutIntent, listCheckoutIntents } from "./checkout-intents";
import { connectivity, useConnectivity } from "./connectivity";
import { offlineStorageAvailable, type CheckoutIntent, type OutboxEntry } from "./offline-db";
import { publishOfflineEvent, subscribeOfflineEvents } from "./offline-events";
import { offlineKeys, useOpenSyncExceptions } from "./offline-queries";
import {
  countForeignSales,
  listOutbox,
  markOutboxBlocked,
  markOutboxException,
  pruneSettledExceptions,
  recordOutboxFailure,
  removeFromOutbox,
  requeueOutboxEntry,
} from "./outbox-repository";
import { parkedSales, type ParkedSaleView } from "./parked-sales";
import { backoffDelay, syncOutbox, type SettledCheckout, type SyncReport, type SyncStore } from "./sync-service";

// Runs the sync: when the API becomes reachable, when the tab comes back to the foreground, on
// opening the POS and on "Đồng bộ ngay". A failed pass retries with backoff (1 s doubling to 60 s,
// with jitter), and while that retry is due nothing but "Đồng bộ ngay" starts another pass; only a
// request with no answer at all marks the API unreachable. An ended session stops it until the
// owner signs in again, and nothing leaves the device without the server's answer. Tabs of the same
// store take turns through a Web Lock, so one sale is never uploaded by two tabs at once, and tell
// each other what changed so their outbox and cap stay current.
//
// Only the signed-in tenant's sales and checkouts are listed and sent. Those another tenant left on
// this device (signed out, sandbox reset) are only counted, and wait for a sign-in to that tenant.

const SYNC_LOCK = "tally-pos-offline-sync";

/** "Đồng bộ xong: n giao dịch ghi sổ[, m vào hàng đợi ngoại lệ]. Mỗi giao dịch chỉ ghi một lần." */
export function syncDoneMessage(posted: number, parked: number): string {
  return `Đồng bộ xong: ${posted} giao dịch ghi sổ${parked > 0 ? `, ${parked} vào hàng đợi ngoại lệ` : ""}. Mỗi giao dịch chỉ ghi một lần.`;
}

export function blockedSalesMessage(count: number): string {
  return `${count} giao dịch ngoại tuyến bị máy chủ từ chối và đang chờ xử lý trên máy. Các giao dịch sau vẫn được đồng bộ.`;
}

/** Sales another tenant left on this device; they go only once that tenant signs in here again. */
export function foreignSalesMessage(count: number): string {
  return `Máy còn ${count} giao dịch ngoại tuyến của cửa hàng khác, chưa đồng bộ. Chúng chỉ được gửi khi đăng nhập lại đúng cửa hàng đó trên máy này; đừng xóa dữ liệu trình duyệt trước khi đồng bộ.`;
}

/** A refused checkout posted nothing, but the counter took the money: the cashier must know. */
export function refusedCheckoutMessage(total: string, error: string | null): string {
  return `Giao dịch ${total} gửi lúc mất mạng chưa được ghi sổ${error ? `: ${error}` : "."} Kiểm tra lại tiền đã thu và bán lại nếu khách đã lấy hàng.`;
}

/** Runs `fn` holding the store's sync lock; null when another tab holds it. */
async function withSyncLock<T>(fn: () => Promise<T>): Promise<T | null> {
  if (typeof navigator === "undefined" || !("locks" in navigator)) return fn();
  return navigator.locks.request(SYNC_LOCK, { ifAvailable: true }, (lock) => (lock ? fn() : null));
}

function storeFor(tenantId: string): SyncStore {
  return {
    list: () => listOutbox(tenantId),
    remove: removeFromOutbox,
    markException: markOutboxException,
    markBlocked: markOutboxBlocked,
    recordFailure: recordOutboxFailure,
    listIntents: () => listCheckoutIntents(tenantId),
    forgetIntent: forgetCheckoutIntent,
  };
}

function announceCheckout(c: SettledCheckout): void {
  if (c.posted) {
    const after = ` · ${vnd(c.total)}: giao dịch gửi lúc mất mạng, chỉ ghi một lần.`;
    toast.success(c.docNo ? withDoc("Đã ghi sổ ", c.docNo, after) : `Đã ghi sổ${after}`);
    return;
  }
  toast.error(refusedCheckoutMessage(vnd(c.total), c.error));
}

export interface OfflineSync {
  /** Every sale of this tenant still on the device, oldest first. */
  outbox: readonly OutboxEntry[];
  /** Those not sent yet ("Chờ đồng bộ"). */
  pending: readonly OutboxEntry[];
  /** Those the server refused for good, waiting for the cashier. */
  blocked: readonly OutboxEntry[];
  /** Online checkouts of this tenant whose answer was lost, sent again before the outbox. */
  intents: readonly CheckoutIntent[];
  /** Sales another tenant left on this device. */
  foreign: number;
  /** The exception queue (open). */
  parked: readonly ParkedSaleView[];
  syncing: boolean;
  /** "Đồng bộ ngay". */
  sync: () => void;
  /** "Gửi lại" a blocked sale: it waits again and a sync starts. */
  requeue: (offlineClientId: string) => Promise<void>;
  /** "Xóa khỏi máy" a blocked sale; the cashier reconciles its cash by hand. */
  dropBlocked: (offlineClientId: string) => Promise<void>;
}

export function useOfflineSync(tenantId: string | null): OfflineSync {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const { state } = useConnectivity();
  const enabled = tenantId !== null && offlineStorageAvailable();
  const tenantKey = tenantId ?? "";

  const outboxQuery = useQuery({
    queryKey: offlineKeys.outbox(tenantKey),
    queryFn: () => listOutbox(tenantKey),
    enabled,
    networkMode: "always",
    staleTime: Infinity,
  });
  const intentsQuery = useQuery({
    queryKey: offlineKeys.intents(tenantKey),
    queryFn: () => listCheckoutIntents(tenantKey),
    enabled,
    networkMode: "always",
    staleTime: Infinity,
  });
  const foreignQuery = useQuery({
    queryKey: offlineKeys.foreign(tenantKey),
    queryFn: () => countForeignSales(tenantKey),
    enabled,
    networkMode: "always",
    staleTime: Infinity,
  });
  const outbox = useMemo(() => outboxQuery.data ?? [], [outboxQuery.data]);
  const pending = useMemo(() => outbox.filter((e) => e.status === "pending"), [outbox]);
  const blocked = useMemo(() => outbox.filter((e) => e.status === "blocked"), [outbox]);
  const intents = useMemo(() => intentsQuery.data ?? [], [intentsQuery.data]);
  const exceptions = useOpenSyncExceptions(tenantId !== null);
  const parked = useMemo(() => parkedSales(exceptions.data?.items, outbox), [exceptions.data, outbox]);

  const refreshDevice = useCallback(
    async (id: string) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: offlineKeys.outbox(id) }),
        queryClient.invalidateQueries({ queryKey: offlineKeys.intents(id) }),
        queryClient.invalidateQueries({ queryKey: offlineKeys.foreign(id) }),
      ]);
    },
    [queryClient],
  );

  const [syncing, setSyncing] = useState(false);
  const running = useRef(false);
  const attempt = useRef(0);
  const retry = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The offline window is renewed once per connection, not on every pass.
  const granted = useRef(false);
  const runRef = useRef<(manual: boolean) => Promise<void>>(async () => undefined);

  const clearRetry = () => {
    if (retry.current !== null) clearTimeout(retry.current);
    retry.current = null;
  };
  const scheduleRetry = useCallback(() => {
    clearRetry();
    retry.current = setTimeout(() => {
      retry.current = null;
      void runRef.current(false);
    }, backoffDelay(attempt.current++));
  }, []);

  const run = useCallback(
    async (manual: boolean) => {
      if (!enabled || tenantId === null || running.current) return;
      // A retry is due: automatic triggers wait for it, so failures keep their backoff.
      if (!manual && retry.current !== null) return;
      if (connectivity.getState() !== "online") {
        if (manual) toast.error("Chưa kết nối được máy chủ. Giao dịch vẫn nằm trên máy và sẽ đồng bộ khi có mạng.");
        return;
      }
      clearRetry();
      running.current = true;
      setSyncing(true);
      let report: SyncReport | null;
      try {
        report = await withSyncLock(() => syncOutbox(client, storeFor(tenantId), { grant: !granted.current }));
      } catch (e) {
        // The device storage failed; the outbox is untouched and the next pass tries again.
        report = { posted: 0, parked: 0, blocked: 0, checkouts: [], granted: false, stopped: "failed", error: errorMessage(e) };
      } finally {
        running.current = false;
        setSyncing(false);
      }
      if (report === null) {
        // Another tab of the store is syncing: look again once it is likely done.
        scheduleRetry();
        return;
      }
      if (report.granted) granted.current = true;
      for (const c of report.checkouts) {
        announceCheckout(c);
        publishOfflineEvent({ type: "intent-settled", tenantId, key: c.key, posted: c.posted });
      }
      await refreshDevice(tenantId);
      if (report.posted + report.parked + report.blocked + report.checkouts.length > 0) {
        publishOfflineEvent({ type: "changed", tenantId });
        for (const key of POSTING_KEYS) void queryClient.invalidateQueries({ queryKey: key });
      }
      if (report.posted + report.parked > 0) toast.success(syncDoneMessage(report.posted, report.parked));
      if (report.blocked > 0) toast.error(blockedSalesMessage(report.blocked));
      switch (report.stopped) {
        case null:
          attempt.current = 0;
          return;
        case "reauth":
          // The shell asks for the sign-in; the next one runs the sync again.
          void queryClient.invalidateQueries({ queryKey: platformKeys.me() });
          return;
        case "read_only":
          if (manual && report.error) toast.error(report.error);
          return;
        case "network":
          connectivity.reportFailure();
          scheduleRetry();
          return;
        case "server":
        case "failed":
          if (manual && report.error) toast.error(`Chưa đồng bộ được: ${report.error} Hệ thống sẽ tự thử lại.`);
          scheduleRetry();
          return;
      }
    },
    [client, queryClient, enabled, tenantId, scheduleRetry, refreshDevice],
  );
  useEffect(() => {
    runRef.current = run;
  }, [run]);

  useEffect(() => {
    // A new connection renews the offline window again before its first upload.
    if (state !== "online") granted.current = false;
  }, [state]);

  const hasWork = pending.length > 0 || intents.length > 0;
  useEffect(() => {
    if (state === "online" && hasWork) void runRef.current(false);
  }, [state, hasWork, tenantId]);

  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === "visible") void runRef.current(false);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      clearRetry();
    };
  }, []);

  // Another tab (or this one) saved, synced or settled something: read the device again.
  useEffect(() => {
    if (!enabled || tenantId === null) return;
    return subscribeOfflineEvents((event) => {
      if (event.tenantId === tenantId) void refreshDevice(tenantId);
    });
  }, [enabled, tenantId, refreshDevice]);

  // Parked sales a manager settled (on any device) leave this device's outbox. Only a complete
  // list decides; a sale parked after the list was read is still open on the server either way.
  useEffect(() => {
    const data = exceptions.data;
    if (!enabled || tenantId === null || !data?.complete) return;
    const open = new Set(data.items.map((x) => x.offline_client_id));
    pruneSettledExceptions(tenantId, open)
      .then((n) => (n > 0 ? publishOfflineEvent({ type: "changed", tenantId }) : undefined))
      .catch((e: unknown) => toast.error(`Không cập nhật được giao dịch ngoại tuyến trên máy: ${errorMessage(e)}`));
  }, [exceptions.data, enabled, tenantId]);

  const sync = useCallback(() => void runRef.current(true), []);
  const requeue = useCallback(
    async (offlineClientId: string) => {
      if (tenantId === null) return;
      await requeueOutboxEntry(offlineClientId);
      publishOfflineEvent({ type: "changed", tenantId });
      await runRef.current(true);
    },
    [tenantId],
  );
  const dropBlocked = useCallback(
    async (offlineClientId: string) => {
      if (tenantId === null) return;
      await removeFromOutbox(offlineClientId);
      publishOfflineEvent({ type: "changed", tenantId });
    },
    [tenantId],
  );
  return { outbox, pending, blocked, intents, foreign: foreignQuery.data ?? 0, parked, syncing, sync, requeue, dropBlocked };
}
