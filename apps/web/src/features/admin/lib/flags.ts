import type { FeatureFlag, FeatureFlagList } from "../api/schemas";

export interface FlagRow {
  key: string;
  /** The global value; undefined when only stores have one. */
  global: FeatureFlag | undefined;
  /** Per-store values, which win over the global one. */
  stores: FeatureFlag[];
}

/**
 * Stored flag values grouped by key, plus a row for every key the server reads that has no value
 * yet; keys in alphabetical order.
 */
export function flagRows({ data, known_keys }: FeatureFlagList): FlagRow[] {
  const rows = new Map<string, FlagRow>(known_keys.map((key) => [key, { key, global: undefined, stores: [] }]));
  for (const f of data) {
    const row = rows.get(f.key) ?? { key: f.key, global: undefined, stores: [] };
    if (f.tenant_id === null) row.global = f;
    else row.stores.push(f);
    rows.set(f.key, row);
  }
  return [...rows.values()].sort((a, b) => a.key.localeCompare(b.key));
}

/** The value a store gets: its own when set, else the global one, else off. */
export function effectiveFor(row: FlagRow, tenantId: string): { enabled: boolean; own: boolean } {
  const own = row.stores.find((f) => f.tenant_id === tenantId);
  if (own) return { enabled: own.enabled, own: true };
  return { enabled: row.global?.enabled ?? false, own: false };
}

export const onOff = (enabled: boolean) => (enabled ? "Bật" : "Tắt");

/** Value column of a flag change: on, off, or the removal of a store's own value. */
export const changeText = (enabled: boolean | null) => (enabled === null ? "Đã gỡ giá trị riêng" : onOff(enabled));
