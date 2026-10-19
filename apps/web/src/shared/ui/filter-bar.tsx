import type { ReactNode } from "react";

/** Prototype `.filters`: search box, chips and selects above a table. */
export function FilterBar({ children }: { children: ReactNode }) {
  return <div className="filters">{children}</div>;
}

export interface SearchInputProps {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  /** Accessible name; defaults to the placeholder. */
  label?: string;
  /** Longest search the endpoint accepts (its `q` maxLength); longer typing is cut off. */
  maxLength?: number;
}

/** Controlled search box; the caller debounces server queries with useDebounce. */
export function SearchInput({ value, onChange, placeholder, label, maxLength }: SearchInputProps) {
  return <input type="search" value={value} placeholder={placeholder} aria-label={label ?? placeholder} maxLength={maxLength} onChange={(e) => onChange(e.target.value)} />;
}
