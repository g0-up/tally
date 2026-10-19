import { Button, ButtonRow } from "./button";
import { DatePicker } from "./date-picker";
import { PERIOD_DATE_LABEL, PERIOD_LABEL, PERIODS, type Period } from "./period";

export interface PeriodBarProps {
  period: Period;
  onPeriodChange: (period: Period) => void;
  /** Anchor date (ISO). */
  date: string;
  onDateChange: (date: string) => void;
  onPrev: () => void;
  onNext: () => void;
  onToday: () => void;
  /** Business date; "Về hôm nay" shows only when the anchor differs from it. */
  today: string;
}

/** Prototype dashboard `.periodbar`: period chips, anchor date, "‹ Kỳ trước" / "Kỳ sau ›" / "Về hôm nay". */
export function PeriodBar({ period, onPeriodChange, date, onDateChange, onPrev, onNext, onToday, today }: PeriodBarProps) {
  return (
    <div className="periodbar">
      <div className="btn-row" role="group" aria-label="Chọn kỳ">
        {PERIODS.map((p) => (
          <button key={p} type="button" className="chip" aria-pressed={p === period} onClick={() => onPeriodChange(p)}>
            {PERIOD_LABEL[p]}
          </button>
        ))}
      </div>
      <label className="f inline">
        <span>{PERIOD_DATE_LABEL[period]}</span>
        <DatePicker value={date} today={today} onChange={(v) => (v ? onDateChange(v) : undefined)} />
      </label>
      <ButtonRow>
        <Button onClick={onPrev}>‹ Kỳ trước</Button>
        <Button onClick={onNext}>Kỳ sau ›</Button>
        {date !== today ? <Button onClick={onToday}>Về hôm nay</Button> : null}
      </ButtonRow>
    </div>
  );
}
