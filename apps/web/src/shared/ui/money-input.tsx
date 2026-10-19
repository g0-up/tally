import { useState, type Ref } from "react";
import { parseMoneyInput, type Money } from "../domain/money";
import { num } from "../lib/format";
import { Input, type InputProps } from "./input";

export interface DecimalInputProps<T extends string> extends Omit<InputProps, "value" | "onChange" | "type" | "inputMode"> {
  /** Current value as an API decimal string, or null when empty / not a number. */
  value: T | null;
  onChange: (value: T | null) => void;
  ref?: Ref<HTMLInputElement>;
}

/**
 * Text input for decimal strings typed the Vietnamese way ("1.250.000", "12,5"). While focused the
 * user's text is kept as typed; on blur it is re-grouped. Values never pass through Number().
 */
function useDecimalText<T extends string>(value: T | null, parse: (text: string) => T | null) {
  const [text, setText] = useState<string | null>(null);
  const shown = text ?? (value === null ? "" : num(value));
  return {
    shown,
    onType: (next: string, emit: (v: T | null) => void) => {
      setText(next);
      emit(parse(next));
    },
    onBlur: () => setText(null),
  };
}

export function DecimalInput<T extends string>({ value, onChange, parse, onBlur, ...rest }: DecimalInputProps<T> & { parse: (text: string) => T | null }) {
  const d = useDecimalText(value, parse);
  return (
    <Input
      {...rest}
      inputMode="decimal"
      autoComplete="off"
      value={d.shown}
      onChange={(e) => d.onType(e.target.value, onChange)}
      onBlur={(e) => {
        d.onBlur();
        onBlur?.(e);
      }}
    />
  );
}

/** VND amount input; emits Money (decimal string) or null. */
export function MoneyInput(props: DecimalInputProps<Money>) {
  return <DecimalInput<Money> {...props} parse={parseMoneyInput} />;
}
