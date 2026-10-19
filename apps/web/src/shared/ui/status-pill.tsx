import { STATUS_GLYPH, statusTone, type StatusTone } from "../domain/status-tone";
import { cx } from "../lib/cx";

/**
 * Prototype `st()`: status text with a glyph so meaning never relies on color. The glyph is
 * aria-hidden; screen readers read the text only. Tone is derived from the label unless given.
 */
export function StatusPill({ label, tone }: { label: string; tone?: StatusTone }) {
  const t = tone ?? statusTone(label);
  return (
    <span className={cx("st", t !== "info" && t)} data-glyph="">
      <span className="st-g" aria-hidden="true">
        {STATUS_GLYPH[t]}
      </span>
      {label}
    </span>
  );
}
