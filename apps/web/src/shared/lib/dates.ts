// Business dates are calendar dates in the store's time zone (Asia/Ho_Chi_Minh, the zone formerly
// named Asia/Saigon), carried as ISO "YYYY-MM-DD" strings. Instants from the API (RFC 3339) are
// shown in that same zone, never in the browser's zone.

export const BUSINESS_TZ = "Asia/Ho_Chi_Minh";

const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DISPLAY_DATE = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/;

const PARTS: Intl.DateTimeFormatOptions = {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
};
const partsFmts = new Map<string, Intl.DateTimeFormat>([[BUSINESS_TZ, new Intl.DateTimeFormat("en-CA", { ...PARTS, timeZone: BUSINESS_TZ })]]);

function partsFmt(timeZone: string): Intl.DateTimeFormat {
  let fmt = partsFmts.get(timeZone);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat("en-CA", { ...PARTS, timeZone });
    partsFmts.set(timeZone, fmt);
  }
  return fmt;
}

function zoned(instant: Date, timeZone: string = BUSINESS_TZ): Record<"year" | "month" | "day" | "hour" | "minute", string> {
  const out = { year: "", month: "", day: "", hour: "", minute: "" };
  for (const p of partsFmt(timeZone).formatToParts(instant)) {
    if (p.type in out) out[p.type as keyof typeof out] = p.value;
  }
  return out;
}

/** True for an IANA time zone the browser knows ("Asia/Saigon" is an alias of the business zone). */
export function isTimeZone(timeZone: string): boolean {
  try {
    partsFmt(timeZone);
    return true;
  } catch {
    return false;
  }
}

/** Calendar date of `now` in the business time zone (or the given one). */
export function todayBusinessDate(now: Date = new Date(), timeZone: string = BUSINESS_TZ): string {
  const z = zoned(now, timeZone);
  return `${z.year}-${z.month}-${z.day}`;
}

/**
 * Milliseconds from `now` until the next business day starts in the time zone. Counts a 24-hour
 * day, so on a daylight-saving change it may fire an hour early; the caller re-reads the date then.
 */
export function msUntilNextBusinessDay(now: Date, timeZone: string = BUSINESS_TZ): number {
  const z = zoned(now, timeZone);
  // Zone offsets are whole minutes, so the seconds within the minute are the same in every zone.
  const intoDay = (Number(z.hour) * 60 + Number(z.minute)) * 60_000 + (((now.getTime() % 60_000) + 60_000) % 60_000);
  return 86_400_000 - intoDay;
}

/** True for a real calendar date in ISO form ("2026-02-30" is false). */
export function isIsoDate(value: string): boolean {
  const m = ISO_DATE.exec(value);
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const t = new Date(Date.UTC(y, mo - 1, d));
  return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d;
}

/** Parses what a user typed as dd/mm/yyyy into an ISO date; null when it is not a real date. */
export function parseDisplayDate(value: string): string | null {
  const m = DISPLAY_DATE.exec(value.trim());
  if (!m) return null;
  const [, day = "", month = "", year = ""] = m;
  const iso = `${year}-${month.padStart(2, "0")}-${day.padStart(2, "0")}`;
  return isIsoDate(iso) ? iso : null;
}

/** Shifts an ISO date by whole days (calendar arithmetic, no time zone involved). */
export function addDays(iso: string, days: number): string {
  const m = ISO_DATE.exec(iso);
  if (!m) throw new RangeError(`Not an ISO date: "${iso}"`);
  const t = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + days));
  return t.toISOString().slice(0, 10);
}

/** Whole days from `from` to `to` (positive when `to` is later). */
export function diffDays(to: string, from: string): number {
  const a = Date.parse(`${to}T00:00:00Z`);
  const b = Date.parse(`${from}T00:00:00Z`);
  if (Number.isNaN(a) || Number.isNaN(b)) throw new RangeError(`Not ISO dates: "${to}", "${from}"`);
  return Math.round((a - b) / 86_400_000);
}

/** First day of the month of an ISO date. */
export function monthStart(iso: string): string {
  return iso.slice(0, 8) + "01";
}

/** An API instant as "dd/mm/yyyy HH:mm" in the business time zone; "—" when missing or invalid. */
export function fdt(instant: string | null | undefined): string {
  if (!instant) return "—";
  const t = new Date(instant);
  if (Number.isNaN(t.getTime())) return "—";
  const z = zoned(t);
  return `${z.day}/${z.month}/${z.year} ${z.hour}:${z.minute}`;
}

/** Business date (ISO) of an API instant, in the business time zone (or the given one). */
export function businessDateOf(instant: string, timeZone: string = BUSINESS_TZ): string {
  const t = new Date(instant);
  if (Number.isNaN(t.getTime())) throw new RangeError(`Not an instant: "${instant}"`);
  return todayBusinessDate(t, timeZone);
}

/** Monday-first 6×7 calendar grid (ISO dates) covering the month of `anchor`. */
export function monthGrid(anchor: string): string[] {
  const first = monthStart(anchor);
  const weekday = (new Date(`${first}T00:00:00Z`).getUTCDay() + 6) % 7; // 0 = Monday
  const start = addDays(first, -weekday);
  return Array.from({ length: 42 }, (_, i) => addDays(start, i));
}

/** First day of the month `delta` months away from the month of `iso`. */
export function shiftMonth(iso: string, delta: number): string {
  const t = new Date(Date.UTC(Number(iso.slice(0, 4)), Number(iso.slice(5, 7)) - 1 + delta, 1));
  return t.toISOString().slice(0, 10);
}
