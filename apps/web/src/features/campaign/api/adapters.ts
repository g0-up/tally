import { isApiError } from "../../../shared/api/api-error";
import { diffDays } from "../../../shared/lib/dates";
import { dec, isDecimalString, sumDec, type Decimal } from "../../../shared/lib/decimal";
import type { CampaignStatus, DemandRow, SeasonReport } from "../schemas";

// The Chiến dịch mùa vụ screen in the prototype's words (prototype `campSummary` and the
// `chien-dich.mua` view): status labels, the figures of a campaign card, the demand columns derived
// from the API projection, and the server refusals mapped onto sentences.

/** "Kế hoạch" has no prototype label (the prototype has no planned campaign). */
export const CAMPAIGN_STATUS_LABELS: Readonly<Record<CampaignStatus, string>> = {
  planning: "Kế hoạch",
  active: "Đang chạy",
  closed: "Đã kết thúc",
};

/** Server refusals of the campaign commands; `not_found` and `invalid_cursor` read the shared sentences. */
export const CAMPAIGN_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  campaign_status_invalid: "Trạng thái chiến dịch đã thay đổi. Tải lại rồi thử lại.",
  campaign_closed: "Chiến dịch đã kết thúc, không sửa checklist được.",
  campaign_name_taken: "Đã có chiến dịch khác cùng tên. Đặt tên khác.",
  attribution_changed: "Chứng từ vừa được gắn chiến dịch ở nơi khác. Thử lại.",
};

/** The same refusal with the sentence above as its message, for the "Chưa ghi sổ." toast. */
export function withCampaignMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = CAMPAIGN_ERROR_MESSAGES[e.code];
  return message === undefined ? e : e.withMessage(message);
}

/** A summary figure of the season report by key; null when absent, empty or not a number. */
export function seasonFigure(report: SeasonReport | undefined, key: string): Decimal | null {
  const value = report?.summary.find((m) => m.key === key)?.value;
  return value !== null && value !== undefined && isDecimalString(value) ? dec(value) : null;
}

/** Days of the season gone by at `today` ("sau 12/151 ngày của mùa"), within 0..total. */
export function seasonDays(startsOn: string, endsOn: string, today: string): { elapsed: number; total: number } {
  const total = diffDays(endsOn, startsOn) + 1;
  const elapsed = Math.min(total, Math.max(0, diffDays(today, startsOn) + 1));
  return { elapsed, total };
}

/** One line of "Nhu cầu hàng mùa Tết theo ngày", read from the API projection of a SKU. */
export interface DemandLine {
  row: DemandRow;
  /** Confirmed undelivered lines and reservations over the range, earlier ones included (backlog). */
  openDemand: Decimal;
  /** Pending purchases with an expected date in the range. */
  incoming: Decimal;
  /** First day with an expected receipt, null without one. */
  firstIncomingOn: string | null;
}

export function demandLine(row: DemandRow): DemandLine {
  const first = row.days.find((d) => dec(d.incoming).gt(0));
  return {
    row,
    openDemand: sumDec(row.days.map((d) => d.demand)),
    incoming: sumDec(row.days.map((d) => d.incoming)),
    firstIncomingOn: first?.date ?? null,
  };
}
