import { businessDateOf } from "../../../shared/lib/dates";
import { fd, vnd } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListLoading } from "../../../shared/ui/list-footer";
import { useColorDirectory, type ColorDirectory } from "../../catalog";
import { usePriceChanges } from "../api/queries";
import { PRICE_SOURCE_LABELS, priceLogRows, type PriceLogRow, type PriceMove } from "../price-edit";
import { LoadError } from "./load-error";

/** Edits the table shows, newest first (prototype `slice(0, 15)`). */
const SHOWN = 15;
const HEADING_STYLE = { margin: "24px 0 10px" } as const;

function Move({ move }: { move: PriceMove | null }) {
  if (move === null) return <>—</>;
  return (
    <>
      {vnd(move.old)} → <b>{vnd(move.next)}</b>
    </>
  );
}

function columns(colors: ColorDirectory): Column<PriceLogRow>[] {
  return [
    { key: "date", header: "Ngày", cell: (r) => fd(businessDateOf(r.at)) },
    {
      key: "sku",
      header: "Hàng",
      cell: (r) => (
        <>
          {r.skuName}
          {r.colorId !== null ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(r.colorId)} />
              {r.cleared ? (
                <>
                  {" "}
                  <span className="hint">về giá chung</span>
                </>
              ) : null}
            </>
          ) : r.allColors ? (
            <>
              {" "}
              <span className="hint">· mọi màu</span>
            </>
          ) : null}
        </>
      ),
    },
    { key: "retail", header: "Giá lẻ", numeric: true, cell: (r) => <Move move={r.retail} /> },
    { key: "wholesale", header: "Giá sỉ", numeric: true, cell: (r) => <Move move={r.wholesale} /> },
    { key: "source", header: "Nơi sửa", cell: (r) => <Badge alt={r.source === "inventory"}>{PRICE_SOURCE_LABELS[r.source]}</Badge> },
    { key: "actor", header: "Người sửa", cell: (r) => r.actor ?? "—" },
  ];
}

export interface PriceHistoryTableProps {
  /** One SKU's history; null lists every SKU. */
  skuId: string | null;
}

/** Prototype `priceLogTable()`: "Lịch sử thay đổi giá bán" with the source of each edit. */
export function PriceHistoryTable({ skuId }: PriceHistoryTableProps) {
  const changes = usePriceChanges(skuId);
  const colors = useColorDirectory();
  const rows = priceLogRows(changes.data ?? []).slice(0, SHOWN);
  return (
    <>
      <h3 className="sec-h" style={HEADING_STYLE}>
        Lịch sử thay đổi giá bán
      </h3>
      {changes.error ? (
        <LoadError what="lịch sử giá" error={changes.error} onRetry={() => void changes.refetch()} />
      ) : changes.isPending ? (
        <ListLoading />
      ) : (
        <DataTable caption="Lịch sử thay đổi giá bán" columns={columns(colors)} rows={rows} rowKey={(r) => r.key} empty="Chưa có thay đổi giá." />
      )}
    </>
  );
}
