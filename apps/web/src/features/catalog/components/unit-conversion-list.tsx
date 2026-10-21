import { Fragment } from "react";
import { money } from "../../../shared/lib/format";
import { StatusPill } from "../../../shared/ui/status-pill";
import { unitConversionLines } from "../api/adapters";
import type { SkuListItem } from "../schemas";

/** Prototype `uomCol` cell: "hộp", then "1 thùng = 12 hộp · 250.000 ₫" (or "tự tính") per larger unit. */
export function UnitConversionList({ sku }: { sku: Pick<SkuListItem, "units"> }) {
  return unitConversionLines(sku).map((line, i) => (
    <Fragment key={line.key}>
      {i > 0 ? <br /> : null}
      {line.text}
      {line.isBase ? null : <> · {line.price === null ? <span className="hint">tự tính</span> : money(line.price)}</>}
      {line.inactive ? (
        <>
          {" "}
          <StatusPill label="Ngừng dùng" tone="mute" />
        </>
      ) : null}
    </Fragment>
  ));
}
