import type { ReactNode } from "react";

/** Prototype `.pos`: catalog on the left (scan + tiles), the cart card on the right; one column ≤1100px. */
export function PosLayout({ catalog, cart }: { catalog: ReactNode; cart: ReactNode }) {
  return (
    <div className="pos">
      <div className="stack">{catalog}</div>
      <section className="card stack" style={{ gap: 14 }} aria-label="Giỏ hàng">
        {cart}
      </section>
    </div>
  );
}
