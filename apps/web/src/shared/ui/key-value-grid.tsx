import type { ReactNode } from "react";

export interface KeyValueItem {
  label: string;
  value: ReactNode;
}

/** Prototype `kv()`: label/value pairs in an auto-fit grid (drawer headers, document facts). */
export function KeyValueGrid({ items }: { items: readonly KeyValueItem[] }) {
  return (
    <div className="kv">
      {items.map((it) => (
        <div key={it.label}>
          <b>{it.label}</b>
          <span>{it.value}</span>
        </div>
      ))}
    </div>
  );
}
