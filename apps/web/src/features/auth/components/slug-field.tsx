import type { ChangeEvent, Ref } from "react";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { useSlugCheck } from "../api/queries";
import { SLUG_STATUS, isSlugFormatValid } from "../lib/slug";
import { MESSAGES } from "../schemas";

export interface SlugFieldProps {
  value: string;
  onChange: (slug: string) => void;
  onBlur: () => void;
  name: string;
  ref: Ref<HTMLInputElement>;
  /** Validation message of the form (zod or API). */
  error: string | undefined;
  /** ".tally.vn": shown after the input; null while unknown. */
  hostSuffix: string | null;
}

/**
 * "Địa chỉ cửa hàng": the subdomain input with the fixed host suffix and a live availability
 * line (free / invalid / reserved / taken with a free suggestion), announced politely.
 */
export function SlugField({ value, onChange, onBlur, name, ref, error, hostSuffix }: SlugFieldProps) {
  const check = useSlugCheck(value);
  const wellFormed = isSlugFormatValid(value);
  // The server's answer counts only once it is about the value currently typed.
  const answer = wellFormed && !check.checking ? check.data : undefined;

  let status: string | null = null;
  let bad = false;
  if (value !== "" && !wellFormed) {
    status = error ? null : MESSAGES.slugInvalid;
  } else if (check.checking) {
    status = SLUG_STATUS.checking;
  } else if (wellFormed && check.isError) {
    status = SLUG_STATUS.checkFailed;
  } else if (answer?.available) {
    status = SLUG_STATUS.available;
  } else if (answer) {
    status = answer.reason === "reserved" ? MESSAGES.slugReserved : answer.reason === "taken" ? MESSAGES.slugTaken : MESSAGES.slugInvalid;
    bad = true;
  }
  // The form error already says the same thing; the line keeps only the suggestion then.
  if (error && bad) status = null;
  const suggestion = answer && !answer.available ? (answer.suggestion ?? null) : null;

  return (
    <div>
      <Field label="Địa chỉ cửa hàng" error={error}>
        <span className="in-affix">
          <Input
            ref={ref}
            name={name}
            value={value}
            onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value.toLowerCase())}
            onBlur={onBlur}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            inputMode="url"
            maxLength={63}
            invalid={error !== undefined || bad}
            aria-describedby="slug-status"
          />
          {hostSuffix ? (
            <span className="affix">{hostSuffix}</span>
          ) : null}
        </span>
      </Field>
      <p id="slug-status" className={bad ? "err-text" : "hint"} aria-live="polite">
        {status}
        {suggestion ? (
          <>
            {" "}
            <button type="button" className="lnk" onClick={() => onChange(suggestion)}>
              Dùng “{suggestion}”
            </button>
          </>
        ) : null}
      </p>
    </div>
  );
}
