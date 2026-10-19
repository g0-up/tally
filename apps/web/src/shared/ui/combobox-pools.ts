import type { HttpClient } from "../api/http";
import type { ColorCode } from "../domain/color";
import { matchesAllTokens, searchTokens } from "../lib/textnorm";

// Search pools for the shared Combobox. Features own the data, so each feature registers the
// adapter for its pool from its index (catalog → sell/buy/stock/kit, partners → party). The
// Combobox only knows the pool name.

export type ComboPool = "sell" | "buy" | "stock" | "kit" | "party";

export const COMBO_POOLS: readonly ComboPool[] = ["sell", "buy", "stock", "kit", "party"];

/** Cap on rendered options; the popup asks the user to type more when it is reached. */
export const COMBO_MAX_RESULTS = 50;

export interface ComboItem {
  id: string;
  /** Bold first line of the option ("Bánh quy bơ Danisa 454g", "Tạp hóa Minh Châu"). */
  name: string;
  /** Text shown in the input once picked; defaults to name (products use "SP001 · name"). */
  inputLabel?: string;
  /** Second line ("SP001 · có thể bán 24 hộp · 125.000 ₫", "Đại lý · 0903… · đang nợ …"). */
  detail?: string;
  /** Product colors, rendered as dots after the name. */
  colors?: readonly ColorCode[];
  /** Extra searchable text that is not displayed (barcode, phone, group). */
  keywords?: readonly string[];
}

export interface ComboSearchContext {
  client: HttpClient;
  /** Raw text typed by the user; empty when the field was just focused. */
  query: string;
  signal: AbortSignal;
}

export interface ComboAdapter {
  /** Returns candidates for the query; the Combobox caps the list at COMBO_MAX_RESULTS. */
  search: (ctx: ComboSearchContext) => Promise<readonly ComboItem[]>;
}

const adapters = new Map<ComboPool, ComboAdapter>();

/** Registers the adapter for a pool; the returned function removes it again. */
export function registerComboPool(pool: ComboPool, adapter: ComboAdapter): () => void {
  adapters.set(pool, adapter);
  return () => {
    if (adapters.get(pool) === adapter) adapters.delete(pool);
  };
}

export function getComboPool(pool: ComboPool): ComboAdapter | undefined {
  return adapters.get(pool);
}

/** Test helper. */
export function clearComboPools(): void {
  adapters.clear();
}

/**
 * Accent-insensitive client filter ("banh" finds "Bánh"): every token must occur in the id, name,
 * input label, detail or keywords. Capped at COMBO_MAX_RESULTS.
 */
export function filterComboItems(items: readonly ComboItem[], query: string): ComboItem[] {
  const tokens = searchTokens(query);
  const out: ComboItem[] = [];
  for (const it of items) {
    if (matchesAllTokens(tokens, it.id, it.name, it.inputLabel, it.detail, ...(it.keywords ?? []))) {
      out.push(it);
      if (out.length === COMBO_MAX_RESULTS) break;
    }
  }
  return out;
}

/** Empty-state text quoting what the user typed. */
export function comboEmptyText(query: string): string {
  return `Không tìm thấy “${query}”.`;
}
