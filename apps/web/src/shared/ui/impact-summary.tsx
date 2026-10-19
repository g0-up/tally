import type { ReactNode } from "react";

export interface ImpactItem {
  label: string;
  value: ReactNode;
}

/** Prototype `impact()`: what a command will change, shown before the owner posts it. */
export function ImpactSummary({ items }: { items: readonly ImpactItem[] }) {
  return (
    <div className="impact">
      {items.map((it) => (
        <div key={it.label}>
          <b>{it.label}</b>
          <span>{it.value}</span>
        </div>
      ))}
    </div>
  );
}

/** Payment box (`.paybox`): impact summary whose third figure is emphasised (amount still owed). */
export function PayBox({ items }: { items: readonly ImpactItem[] }) {
  return (
    <div className="paybox">
      <ImpactSummary items={items} />
    </div>
  );
}
