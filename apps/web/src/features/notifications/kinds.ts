import type { StatusTone } from "../../shared/domain/status-tone";
import type { Notification, Severity } from "./schemas";

// Notification kinds in the prototype's words, and the screen each one opens: the web route the
// API puts in `action_path`.

/** Chip and "Loại" label of each kind, in the order the chips appear. */
export const KIND_LABELS: Readonly<Record<string, string>> = {
  receivable_overdue: "Nợ quá hạn",
  payable_due_soon: "Sắp phải trả NCC",
  lot_expiring: "Sắp hết hạn",
  lot_expired: "Hết hạn – chặn bán",
  below_min_stock: "Dưới tồn tối thiểu",
  surplus_stock: "Vượt tồn tối đa",
  pos_sync_exception: "Lỗi đồng bộ POS",
  statement_disputed: "Bảng kê tranh chấp",
  subscription_status: "Thuê bao",
  export_ready: "File xuất sẵn sàng",
};

const OTHER_LABEL = "Khác";

export const kindLabel = (kind: string): string => KIND_LABELS[kind] ?? OTHER_LABEL;

const KIND_ORDER = Object.keys(KIND_LABELS);

/** Kinds that always have a chip, with 0 when none is active, so the owner sees what is clear. */
const FIXED_KINDS: readonly string[] = ["receivable_overdue", "payable_due_soon", "lot_expiring", "lot_expired", "below_min_stock", "pos_sync_exception"];

/** The fixed kinds and any other kind present in `list`, known ones in chip order, with how many notifications each has. */
export function kindCounts(list: readonly Notification[]): { kind: string; count: number }[] {
  const counts = new Map<string, number>(FIXED_KINDS.map((k) => [k, 0]));
  for (const n of list) counts.set(n.kind, (counts.get(n.kind) ?? 0) + 1);
  const rank = (k: string) => {
    const i = KIND_ORDER.indexOf(k);
    return i === -1 ? KIND_ORDER.length : i;
  };
  return [...counts].sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b)).map(([kind, count]) => ({ kind, count }));
}

export const SEVERITY_TONE: Readonly<Record<Severity, StatusTone>> = { info: "info", warn: "warn", bad: "bad" };

/**
 * The screen that handles a notification; null when there is none (an export is downloaded, not
 * opened). Only a path of this app is followed.
 */
export function notificationTarget(n: Pick<Notification, "action_path">): string | null {
  const to = n.action_path;
  return to && to.startsWith("/") && !to.startsWith("//") ? to : null;
}
