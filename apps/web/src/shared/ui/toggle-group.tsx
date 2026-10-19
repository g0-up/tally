export interface ToggleOption<V extends string> {
  value: V;
  label: string;
}

export interface ToggleGroupProps<V extends string> {
  options: ReadonlyArray<ToggleOption<V>>;
  value: V;
  onChange: (value: V) => void;
  /** Accessible name of the group. */
  label: string;
}

/** Prototype `chips()`: single-choice pill buttons with aria-pressed. */
export function ToggleGroup<V extends string>({ options, value, onChange, label }: ToggleGroupProps<V>) {
  return (
    <div role="group" aria-label={label} className="chip-group">
      {options.map((o) => (
        <button key={o.value} type="button" className="chip" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}
