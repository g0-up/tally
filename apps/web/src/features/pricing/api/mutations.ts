import { useRef } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage, isReauthRequired, isSessionExpired } from "../../../shared/api/api-error";
import type { HttpClient } from "../../../shared/api/http";
import type { Money } from "../../../shared/domain/money";
import { outcomeUnknown } from "../../../shared/hooks/create-once";
import { PartialCommitError, useCommit } from "../../../shared/hooks/use-commit";
import { toDecimalString } from "../../../shared/lib/decimal";
import { toast } from "../../../shared/ui/toast-store";
import { skuKeys } from "../../catalog";
import { salePriceRequests, validatePriceDraft, type CurrentPrices, type PriceDraft } from "../price-edit";
import type { PriceChangeSource } from "../schemas";
import { pricingKeys } from "./queries";

/** One `POST /skus/{id}/sale-price` body. */
export interface SalePriceBody {
  kind: "retail" | "wholesale";
  /** Null removes the own price of `color_id`. */
  price: Money | null;
  color_id?: string;
  apply_all_colors?: boolean;
  source: PriceChangeSource;
}

/**
 * What one intent has sent to one sale-price slot (SKU, list, color scope). Each value the intent
 * moves the slot to is a write of its own with the next number, so the key never names the value
 * alone: going back to an earlier price after another one is a new write, not a replay of the first.
 */
interface SalePriceSlot {
  n: number;
  /** The value of the write whose answer was lost; resent under the same key. */
  sent: string | null;
  /** The value the last answered write stored. */
  written: string | null;
}

/** The sale-price writes of one intent, by slot. Start a new map with each intent. */
export type SalePriceWrites = Map<string, SalePriceSlot>;

/** A price as one canonical decimal string ("18000.00" and "18000" are the same write). */
const priceValue = (price: Money | null) => (price === null ? "none" : toDecimalString(price));

/**
 * Writes one base sale price under its own key derived from the intent's, unless the intent's last
 * answered write of that slot stored the same value. A lost answer keeps the key, so a retry
 * replays what the server stored; any other value takes the next number.
 */
export async function postSalePrice(client: HttpClient, intentKey: string, skuId: string, body: SalePriceBody, writes: SalePriceWrites): Promise<void> {
  const scope = body.color_id ?? (body.apply_all_colors ? "all-colors" : "general");
  const slotId = `${skuId}:${body.kind}:${scope}`;
  const value = priceValue(body.price);
  const slot = writes.get(slotId) ?? { n: 0, sent: null, written: null };
  writes.set(slotId, slot);
  if (slot.sent === null && slot.written === value) return;
  if (slot.sent !== value) {
    slot.n += 1;
    slot.sent = value;
  }
  const key = `${intentKey}:sale-price:${slotId}:${body.source}:${value}:${slot.n}`;
  const price = body.price === null ? null : (value as Money);
  try {
    await client.post<unknown>(`/skus/${skuId}/sale-price`, { ...body, price }, { idempotencyKey: key });
  } catch (e) {
    if (!outcomeUnknown(e)) slot.sent = null;
    throw e;
  }
  slot.sent = null;
  slot.written = value;
}

export interface SaveSalePriceOptions {
  sku: CurrentPrices & { id: string };
  source: PriceChangeSource;
  after: () => void;
}

const LIST_LABEL = { retail: "giá bán lẻ", wholesale: "giá sỉ" } as const;

/**
 * "Lưu giá": checks the draft like the prototype, then writes the retail and wholesale base prices,
 * one request per list. A retry skips the list that went through and sends the one still missing;
 * once one list is stored, a failure says so instead of "Chưa ghi sổ.".
 */
export function useSaveSalePrice({ sku, source, after }: SaveSalePriceOptions) {
  const client = useApiClient();
  const progress = useRef<{ key: string; writes: SalePriceWrites } | null>(null);
  const commit = useCommit<unknown, PriceDraft>({
    mutationFn: async (draft, { idempotencyKey }) => {
      if (progress.current?.key !== idempotencyKey) progress.current = { key: idempotencyKey, writes: new Map() };
      const { writes } = progress.current;
      for (const body of salePriceRequests(draft, sku, source)) {
        try {
          await postSalePrice(client, idempotencyKey, sku.id, body, writes);
        } catch (e) {
          const wrote = [...writes.values()].some((slot) => slot.written !== null);
          if (!wrote || isReauthRequired(e) || isSessionExpired(e)) throw e;
          throw new PartialCommitError(
            outcomeUnknown(e)
              ? `Đã lưu một phần giá nhưng chưa rõ đã lưu ${LIST_LABEL[body.kind]} hay chưa. Bấm lại để kiểm tra và lưu tiếp.`
              : `Đã lưu một phần giá nhưng chưa lưu ${LIST_LABEL[body.kind]}: ${errorMessage(e)} Bấm lại để lưu tiếp.`,
          );
        }
      }
      return null;
    },
    success: (_, draft) => (draft.allColors ? "Đã cập nhật giá cho sản phẩm và tất cả màu." : "Đã cập nhật giá chung."),
    invalidate: [skuKeys.all, pricingKeys.all],
    after,
  });
  return {
    pending: commit.pending,
    save: (draft: PriceDraft) => {
      const failed = validatePriceDraft(draft, sku);
      if (failed !== null) {
        toast.commitError(failed);
        return;
      }
      void commit.run(draft);
    },
  };
}
