import { createElement, Fragment, useCallback, useEffect, useRef, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { ERROR_CODES, errorMessage, isApiError, isSessionExpired } from "../../../shared/api/api-error";
import type { operations } from "../../../shared/api/schema";
import type { ExceptionPrompt } from "../../../shared/hooks/use-confirm-exception";
import { runFollowUps, useCommit, type CommitResult } from "../../../shared/hooks/use-commit";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import { toast } from "../../../shared/ui/toast-store";
import { etagOf, skuKeys } from "../../catalog";
import { postSalePrice, pricingKeys, type SalePriceWrites } from "../../pricing";
import { adjustmentSchema, countSchema, reservationSchema, type AdjustmentKind, type Count, type Lot } from "../schemas";
import { INVENTORY_MESSAGES, withInventoryMessage } from "./adapters";
import { ALERT_KEYS, inventoryKeys } from "./queries";

// Write side of the Kho screens. Every posting goes through useCommit (one Idempotency-Key per
// intent) and refreshes stock, lots, the stock card, the dashboard and the notifications.

/** What a stock posting may change. */
export const STOCK_POSTING_KEYS = [inventoryKeys.all, skuKeys.all, ...ALERT_KEYS] as const;

/** Toast text with the document number in bold: `before <b>docNo</b> after`. */
const withDoc = (before: string, docNo: string, after: string): ReactNode => createElement(Fragment, null, before, createElement("b", null, docNo), after);

/**
 * A command run against one row of a list at a time. Switching to another row starts a new intent,
 * so a failed attempt on one row never lends its Idempotency-Key to the next.
 */
export function usePerRowCommit<TData, TVariables>(commit: CommitResult<TData, TVariables>, rowOf: (v: TVariables) => string): CommitResult<TData, TVariables> {
  const last = useRef<string | null>(null);
  return {
    ...commit,
    run: (v) => {
      const row = rowOf(v);
      if (last.current !== row) {
        commit.resetKey();
        last.current = row;
      }
      return commit.run(v);
    },
  };
}

/** Runs a request and maps a refusal onto the prototype sentence. */
async function inventoryCall<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    throw withInventoryMessage(e);
  }
}

/* ---------- color prices ---------- */

export interface ColorPriceVars {
  skuId: string;
  colorId: string;
  colorName: string;
  /** The product's prices, which a color without an own price follows. */
  shared: { retail: Money | null; wholesale: Money | null };
  /** The color's own prices now; null where it follows the product's. */
  own: { retail: Money | null; wholesale: Money | null };
  /**
   * The color's own prices to keep; null removes both. A null wholesale price makes the color
   * follow the product's wholesale price.
   */
  next: { retail: Money; wholesale: Money | null } | null;
}

/** An own price as the server holds it: its value, or "" for none. */
const ownPrice = (m: Money | null) => (m === null ? "" : dec(m).toString());

/**
 * colorPrice: sets or removes one color's own retail and wholesale price (one `sale-price` write
 * per price that changes; a null price removes the own row). The wholesale price never exceeds the
 * retail one in between, so the order of the writes follows the direction of the change. A retry
 * skips the writes whose value the server already holds, and sends a price edited since.
 */
export function useSetColorPrice(after: () => void) {
  const client = useApiClient();
  const progress = useRef<{ key: string; done: Map<string, string>; writes: SalePriceWrites; wrote: boolean } | null>(null);
  return useCommit({
    mutationFn: async (v: ColorPriceVars, { idempotencyKey }) => {
      if (progress.current?.key !== idempotencyKey) {
        progress.current = { key: idempotencyKey, done: new Map([["retail", ownPrice(v.own.retail)], ["wholesale", ownPrice(v.own.wholesale)]]), writes: new Map(), wrote: false };
      }
      const p = progress.current;
      const write = (kind: "retail" | "wholesale", price: Money | null) => ({
        id: kind,
        label: kind === "retail" ? "lưu giá bán lẻ" : "lưu giá sỉ",
        value: ownPrice(price),
        run: () => postSalePrice(client, idempotencyKey, v.skuId, { kind, color_id: v.colorId, price, source: "inventory" }, p.writes),
      });
      const retail = v.next?.retail ?? null;
      const r = write("retail", retail);
      const w = write("wholesale", v.next?.wholesale ?? null);
      const wholesaleNow = p.done.get("wholesale") || v.shared.wholesale;
      const wholesaleFirst = retail === null || (wholesaleNow !== null && dec(retail).lt(wholesaleNow));
      const steps = (wholesaleFirst ? [w, r] : [r, w]).filter((s) => p.done.get(s.id) !== s.value);
      // Until one write went through nothing is stored: a refusal then is a plain "Chưa ghi sổ.".
      let rest = steps;
      const [first, ...others] = steps;
      if (first && !p.wrote) {
        await inventoryCall(first.run);
        p.done.set(first.id, first.value);
        p.wrote = true;
        rest = others;
      }
      const done = new Set<string>();
      try {
        await runFollowUps(rest, done, (missing, reason) => `Đã lưu một phần giá ${v.colorName} nhưng chưa ${missing}: ${reason}`, withInventoryMessage);
      } finally {
        for (const s of rest) if (done.has(s.id)) p.done.set(s.id, s.value);
      }
      return v;
    },
    success: (_, v) => (v.next === null ? `Đã đưa ${v.colorName} về giá chung.` : `Đã lưu giá riêng ${v.colorName}.`),
    invalidate: [pricingKeys.all, skuKeys.all, inventoryKeys.stock],
    after: () => {
      progress.current = null;
      after();
    },
  });
}

/* ---------- reservations ---------- */

type ReleaseBody = operations["releaseReservation"]["requestBody"]["content"]["application/json"];

/**
 * resRelease: frees all of an open hold (no `qty`, as the prototype releases the whole hold); the
 * reason is stored on the hold and in the audit log.
 */
export function useReleaseReservation(after: () => void, setFieldError: (field: string, message: string, reason: string) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { id: string; reason: string }) => inventoryCall(() => client.post(`/reservations/${v.id}:release`, { reason: v.reason } satisfies ReleaseBody, { schema: reservationSchema })),
    success: () => "Đã giải phóng.",
    invalidate: STOCK_POSTING_KEYS,
    after,
    setFieldError,
  });
}

/* ---------- adjustments ---------- */

export interface AdjustmentLineRequest {
  sku_id: string;
  qty: Qty;
  color_id?: string;
  uncolored?: boolean;
  lot_id?: string;
}

export interface AdjustmentVars {
  kind: AdjustmentKind;
  reasonId: string | null;
  /** Free-text reason when the reason catalog has none of this kind. */
  note?: string;
  lines: AdjustmentLineRequest[];
  /** Product name for the exception prompt. */
  name: string;
}

/** The whole of one lot (one color of it, or its uncolored stock) as an adjustment line. */
export const lotLine = (lot: Pick<Lot, "id" | "sku_id" | "qty" | "color_id">): AdjustmentLineRequest => ({
  sku_id: lot.sku_id,
  qty: lot.qty,
  lot_id: lot.id,
  ...(lot.color_id ? { color_id: lot.color_id } : { uncolored: true }),
});

/** Owner confirmations a stock adjustment may need, by the API's exception kind. */
const ADJUSTMENT_EXCEPTION_TITLES: Readonly<Record<string, string>> = {
  stock_disposal: "Xác nhận tiêu hủy hàng",
  quarantine_release: "Xác nhận giải phóng hàng cách ly",
};

export const adjustmentBody = (v: AdjustmentVars) => ({ kind: v.kind, ...(v.reasonId ? { reason_id: v.reasonId } : {}), ...(v.note ? { note: v.note } : {}), lines: v.lines });

/**
 * newADJ, destroyLot, qOk, qNo: one stock adjustment (`POST /stock-adjustments`). A disposal or
 * a quarantine release asks the owner to confirm and is bound to the SKU it moves.
 */
export function useCreateAdjustment(success: (docNo: string) => ReactNode, after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: AdjustmentVars, { idempotencyKey, exceptionConfirmationId }) =>
      inventoryCall(() =>
        client.post("/stock-adjustments", { ...adjustmentBody(v), ...(exceptionConfirmationId ? { exception_confirmation_id: exceptionConfirmationId } : {}) }, { idempotencyKey, schema: adjustmentSchema }),
      ),
    success: (adj) => success(adj.doc_no),
    invalidate: STOCK_POSTING_KEYS,
    after,
    exception: (e, v): ExceptionPrompt | null => {
      const skuId = v.lines[0]?.sku_id;
      if (skuId === undefined) return null;
      return {
        kind: e.exceptionKind,
        subject: { doc_type: "sku", doc_id: skuId, doc_version: 0 },
        payload: adjustmentBody(v),
        title: ADJUSTMENT_EXCEPTION_TITLES[e.exceptionKind] ?? "Xác nhận điều chỉnh kho",
        sub: v.name,
        checkboxLabel: "Tôi xác nhận ghi sổ",
        submitLabel: "Xác nhận",
      };
    },
  });
}

/* ---------- stock counts ---------- */

/** newCount: opens a blind count of the whole store or one category. */
export function useStartCount(after: (count: Count) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { categoryId: string | null }, { idempotencyKey }) =>
      inventoryCall(() =>
        client.post("/stock-counts", { scope: v.categoryId === null ? "all" : "category", ...(v.categoryId ? { category_id: v.categoryId } : {}), blind: true }, { idempotencyKey, schema: countSchema }),
      ),
    success: (c) => withDoc("Đã mở phiên ", c.doc_no, "."),
    invalidate: [inventoryKeys.counts],
    after,
  });
}

export interface CountLineEntry {
  line_id: string;
  counted_qty: Qty | null;
  lot_no?: string;
}

/**
 * Saves counted quantities as they are typed (prototype `change` on each input), one request at
 * a time so each carries the version the previous one returned. `flush` waits for the queue and
 * returns the latest count.
 */
export function useCountLineSaver(count: Count | undefined) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  const latest = useRef<Count | undefined>(count);
  useEffect(() => {
    const cur = latest.current;
    if (count && (!cur || cur.id !== count.id || cur.version < count.version)) latest.current = count;
  }, [count]);

  const send = useCallback(
    async (lines: readonly CountLineEntry[]): Promise<Count | undefined> => {
      const cur = latest.current;
      if (!cur || lines.length === 0) return cur;
      const next = await inventoryCall(() => client.put(`/stock-counts/${cur.id}/lines`, { lines }, { ifMatch: etagOf(cur.version), schema: countSchema }));
      latest.current = next;
      queryClient.setQueryData(inventoryKeys.count(next.id), next);
      return next;
    },
    [client, queryClient],
  );

  const save = useCallback(
    (lines: readonly CountLineEntry[]) => {
      const run = queue.current.then(() => send(lines));
      queue.current = run.catch((e: unknown) => {
        // Stale data and an expired session already have the app-wide notice.
        const handled = isSessionExpired(e) || (isApiError(e) && (e.status === 412 || e.code === ERROR_CODES.preconditionRequired));
        if (!handled) toast.commitError(errorMessage(e));
      });
      return queue.current;
    },
    [send],
  );

  const flush = useCallback(
    async (lines: readonly CountLineEntry[]): Promise<Count | undefined> => {
      await queue.current;
      return send(lines);
    },
    [send],
  );

  return { save, flush };
}

/** "+3", "-2": a variance with its sign. */
export const signedQty = (q: Qty) => `${dec(q).gt(0) ? "+" : dec(q).lt(0) ? "-" : ""}${num(dec(q).abs())}`;

const hasVariance = (c: Count) => (c.lines ?? []).some((l) => l.variance_qty !== null && l.variance_qty !== undefined && !dec(l.variance_qty).isZero());

export interface FinishCountVars {
  /** Counted quantities not saved yet. */
  pending: CountLineEntry[];
}

/**
 * countSubmit: saves what is still typed, finishes the count, and posts it straight away when
 * every line matches the books (prototype "Số đếm khớp sổ, đã đóng phiên."). A finished count is
 * shown at once; when its posting fails, a retry of the same intent posts it without finishing
 * it again.
 */
export function useFinishCount(flush: (lines: readonly CountLineEntry[]) => Promise<Count | undefined>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const progress = useRef<{ key: string; finished: Count } | null>(null);
  return useCommit({
    mutationFn: async (v: FinishCountVars, { idempotencyKey }) => {
      let finished = progress.current?.key === idempotencyKey ? progress.current.finished : null;
      if (!finished) {
        const saved = await flush(v.pending);
        if (!saved) throw new Error(INVENTORY_MESSAGES.countNotLoaded);
        finished = await inventoryCall(() => client.post(`/stock-counts/${saved.id}:finish`, {}, { ifMatch: etagOf(saved.version), schema: countSchema }));
        progress.current = { key: idempotencyKey, finished };
        queryClient.setQueryData(inventoryKeys.count(finished.id), finished);
      }
      if (hasVariance(finished)) return finished;
      const counted = finished;
      let posted = counted;
      await runFollowUps(
        [
          {
            id: "post",
            label: "ghi sổ",
            run: async () => {
              posted = await client.post(`/stock-counts/${counted.id}:post`, {}, { idempotencyKey, ifMatch: etagOf(counted.version), schema: countSchema });
            },
          },
        ],
        new Set(),
        (_, reason) => withDoc("Đã hoàn tất đếm ", counted.doc_no, ` nhưng chưa ghi sổ: ${reason}`),
        withInventoryMessage,
      );
      return posted;
    },
    success: (c) => (c.status === "posted" ? "Số đếm khớp sổ, đã đóng phiên." : "Đã hoàn tất đếm. Kiểm tra chênh lệch rồi xác nhận ghi sổ."),
    // A list, so a count finished but not posted is refreshed too.
    invalidate: STOCK_POSTING_KEYS,
    after: () => {
      progress.current = null;
    },
  });
}

/** countPost: posts the variances; any variance needs the owner's count_variance confirmation. */
export function usePostCount() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (c: Count, { idempotencyKey, exceptionConfirmationId }) =>
      inventoryCall(() =>
        client.post(`/stock-counts/${c.id}:post`, exceptionConfirmationId ? { exception_confirmation_id: exceptionConfirmationId } : {}, {
          idempotencyKey,
          ifMatch: etagOf(c.version),
          schema: countSchema,
        }),
      ),
    success: () => "Đã ghi sổ chênh lệch kiểm kê.",
    invalidate: STOCK_POSTING_KEYS,
    exception: (e, c): ExceptionPrompt => ({
      kind: e.exceptionKind,
      subject: { doc_type: "stock_count", doc_id: c.id, doc_version: c.version },
      payload: {},
      title: `Xác nhận ghi sổ chênh lệch ${c.doc_no}`,
      issues: (c.lines ?? []).flatMap((l) => (l.variance_qty && !dec(l.variance_qty).isZero() ? [{ type: l.sku_name, detail: `Chênh lệch ${signedQty(l.variance_qty)} ${l.unit_name}` }] : [])),
      checkboxLabel: "Tôi xác nhận ghi sổ chênh lệch",
      submitLabel: "Xác nhận & ghi sổ",
    }),
  });
}

/** countReopen: back to counting for the lines that differ (every line when none does). */
export function useRecount() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (c: Count) => {
      const lines = c.lines ?? [];
      const differing = lines.filter((l) => l.variance_qty && !dec(l.variance_qty).isZero()).map((l) => l.id);
      const lineIds = differing.length > 0 ? differing : lines.map((l) => l.id);
      return inventoryCall(() => client.post(`/stock-counts/${c.id}:recount`, { line_ids: lineIds }, { ifMatch: etagOf(c.version), schema: countSchema }));
    },
    success: () => "Đã mở lại để đếm lại.",
    invalidate: (c) => [inventoryKeys.counts, inventoryKeys.count(c.id)],
  });
}
