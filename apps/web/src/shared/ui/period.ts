// Dashboard reporting periods (prototype `P_LABEL`).

export type Period = "day" | "7d" | "month" | "year";

export const PERIOD_LABEL: Readonly<Record<Period, string>> = { day: "Ngày", "7d": "7 ngày", month: "Tháng", year: "Năm" };

/** Label of the anchor-date field for each period. */
export const PERIOD_DATE_LABEL: Readonly<Record<Period, string>> = {
  day: "Ngày",
  "7d": "Đến ngày",
  month: "Trong tháng có ngày",
  year: "Trong năm có ngày",
};

export const PERIODS: readonly Period[] = ["day", "7d", "month", "year"];
