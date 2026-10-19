import { useEffect, useRef, useState, type KeyboardEvent, type Ref } from "react";
import { Popover } from "radix-ui";
import { useEscapeLayer } from "../hooks/use-hotkeys";
import { addDays, isIsoDate, monthGrid, parseDisplayDate, shiftMonth, todayBusinessDate } from "../lib/dates";
import { fd } from "../lib/format";
import { cx } from "../lib/cx";
import { useFieldControl } from "./field-context";

export const DATE_PLACEHOLDER = "dd/mm/yyyy";
const WEEKDAYS = ["T2", "T3", "T4", "T5", "T6", "T7", "CN"] as const;
const WEEKDAY_NAMES = ["Thứ hai", "Thứ ba", "Thứ tư", "Thứ năm", "Thứ sáu", "Thứ bảy", "Chủ nhật"] as const;

export interface DatePickerProps {
  /** ISO business date ("2026-10-12") or null when empty. */
  value: string | null;
  onChange: (value: string | null) => void;
  id?: string;
  name?: string;
  disabled?: boolean;
  required?: boolean;
  invalid?: boolean;
  /** Highlighted "today" in the calendar; defaults to the business date in Asia/Ho_Chi_Minh. */
  today?: string;
  className?: string;
  ref?: Ref<HTMLInputElement>;
  "aria-label"?: string;
  "aria-describedby"?: string;
}

/** Monday-based position of an ISO date in its week (0 = Monday … 6 = Sunday), as the grid shows it. */
function weekdayIndex(iso: string): number {
  return (new Date(`${iso}T00:00:00Z`).getUTCDay() + 6) % 7;
}

/**
 * Date field that always reads and accepts dd/mm/yyyy (never the browser's locale format), with a
 * calendar popover for pointer users. Typing a full valid date commits it; blur restores the
 * canonical text.
 */
export function DatePicker({
  value,
  onChange,
  id,
  name,
  disabled,
  required,
  invalid,
  today,
  className,
  ref,
  "aria-label": ariaLabel,
  "aria-describedby": describedBy,
}: DatePickerProps) {
  const field = useFieldControl({ describedBy, invalid });
  const todayIso = today ?? todayBusinessDate();
  const [text, setText] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [focus, setFocus] = useState<string>(value ?? todayIso);
  const gridRef = useRef<HTMLDivElement>(null);
  const shown = text ?? (value ? fd(value) : "");

  useEscapeLayer(open, () => setOpen(false));

  const focusDay = (iso: string) => gridRef.current?.querySelector<HTMLButtonElement>(`[data-date="${iso}"]`)?.focus();

  // Keyboard moves within the open calendar. The first focus is given in `onOpenAutoFocus`: the
  // portaled grid is not in the page yet when this effect first runs after opening.
  useEffect(() => {
    if (open) focusDay(focus);
  }, [open, focus]);

  const openCalendar = (next: boolean) => {
    if (next) setFocus(value && isIsoDate(value) ? value : todayIso);
    setOpen(next);
  };

  const pick = (iso: string) => {
    onChange(iso);
    setText(null);
    setOpen(false);
  };

  const onGridKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const step: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
    const d = step[e.key];
    if (d !== undefined) {
      e.preventDefault();
      setFocus((f) => addDays(f, d));
    } else if (e.key === "PageUp" || e.key === "PageDown") {
      e.preventDefault();
      setFocus((f) => shiftMonth(f, e.key === "PageUp" ? -1 : 1));
    } else if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      setFocus((f) => addDays(f, e.key === "Home" ? -weekdayIndex(f) : 6 - weekdayIndex(f)));
    }
  };

  const days = monthGrid(focus);
  const month = focus.slice(0, 7);

  return (
    <Popover.Root open={open} onOpenChange={openCalendar}>
      <div className={cx("dp", className)}>
        <input
          ref={ref}
          id={id}
          name={name}
          className="in dp-in"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          placeholder={DATE_PLACEHOLDER}
          aria-label={ariaLabel}
          {...field}
          required={required}
          disabled={disabled}
          value={shown}
          onChange={(e) => {
            const next = e.target.value;
            setText(next);
            if (next.trim() === "") onChange(null);
            else {
              const iso = parseDisplayDate(next);
              if (iso) onChange(iso);
            }
          }}
          onBlur={() => setText(null)}
        />
        <Popover.Trigger asChild>
          <button type="button" className="dp-btn" aria-label="Chọn ngày trên lịch" disabled={disabled}>
            <span aria-hidden="true">▾</span>
          </button>
        </Popover.Trigger>
      </div>
      <Popover.Portal>
        <Popover.Content
          className="dp-pop"
          align="end"
          sideOffset={4}
          collisionPadding={8}
          aria-label="Lịch chọn ngày"
          onEscapeKeyDown={(e) => e.preventDefault()}
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            focusDay(focus);
          }}
        >
          <div className="dp-head">
            <button type="button" className="btn btn-line btn-sm" aria-label="Tháng trước" onClick={() => setFocus((f) => shiftMonth(f, -1))}>
              ‹
            </button>
            <span aria-live="polite">
              Tháng {month.slice(5, 7)}/{month.slice(0, 4)}
            </span>
            <button type="button" className="btn btn-line btn-sm" aria-label="Tháng sau" onClick={() => setFocus((f) => shiftMonth(f, 1))}>
              ›
            </button>
          </div>
          <div className="dp-grid" ref={gridRef} onKeyDown={onGridKey}>
            {WEEKDAYS.map((w, i) => (
              <abbr key={w} title={WEEKDAY_NAMES[i]} aria-hidden="true">
                {w}
              </abbr>
            ))}
            {days.map((iso, i) => (
              <button
                key={iso}
                type="button"
                className="dp-day"
                data-date={iso}
                aria-label={`${WEEKDAY_NAMES[i % 7]}, ${fd(iso)}`}
                aria-pressed={iso === value}
                data-today={iso === todayIso || undefined}
                data-outside={iso.slice(0, 7) !== month || undefined}
                tabIndex={iso === focus ? 0 : -1}
                onClick={() => pick(iso)}
              >
                {Number(iso.slice(8, 10))}
              </button>
            ))}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
