import { dec } from "../../../shared/lib/decimal";
import { fd, num } from "../../../shared/lib/format";
import { CellSub, CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { demandLine, type DemandLine } from "../api/adapters";
import { useDemand } from "../api/queries";
import type { Campaign } from "../schemas";

const CAPTION = "Nhu cầu hàng mùa Tết theo ngày";

/**
 * Prototype demand table on `GET /campaigns/{id}/demand` over the campaign's dates. The API does not
 * name the purchase order behind the earliest receipt, so "Ngày về sớm nhất" shows the date only.
 */
export function DemandTable({ campaign }: { campaign: Campaign | null }) {
  const list = useDemand(campaign?.id ?? null);
  const lines = list.items.map(demandLine);

  const columns: Column<DemandLine>[] = [
    { key: "sku", header: "Hàng", cell: (l) => <CellText main={l.row.name} sub={l.row.code} /> },
    { key: "demand", header: "Đơn đã chốt còn giao", numeric: true, cell: (l) => num(l.openDemand) },
    { key: "available", header: "Có thể bán", numeric: true, cell: (l) => num(l.row.available) },
    {
      key: "incoming",
      header: "PO dự kiến về",
      numeric: true,
      cell: (l) => (
        <>
          {num(l.incoming)}
          {dec(l.row.undated_incoming).gt(0) ? <CellSub>chưa hẹn ngày {num(l.row.undated_incoming)}</CellSub> : null}
        </>
      ),
    },
    { key: "eta", header: "Ngày về sớm nhất", cell: (l) => fd(l.firstIncomingOn) },
    {
      key: "shortage",
      header: "Thiếu sau khi về",
      numeric: true,
      cell: (l) =>
        dec(l.row.max_shortage).gt(0) ? (
          <>
            <b style={{ color: "var(--bad-ink)" }}>{num(l.row.max_shortage)}</b>
            {l.row.first_shortage_on ? <CellSub>từ {fd(l.row.first_shortage_on)}</CellSub> : null}
          </>
        ) : (
          "0"
        ),
    },
  ];

  return (
    <>
      <h3 className="sec-h" style={{ margin: "24px 0 10px" }}>
        {CAPTION}
      </h3>
      {campaign === null ? (
        <p className="hint">Chưa có chiến dịch đang chạy hoặc sắp chạy.</p>
      ) : (
        <>
          <p className="hint" style={{ marginBottom: 8 }}>
            {campaign.name} · {fd(campaign.starts_on)} – {fd(campaign.ends_on)}
          </p>
          {list.isLoading ? <ListLoading /> : <DataTable caption={CAPTION} columns={columns} rows={lines} rowKey={(l) => l.row.sku_id} empty="Không có hàng đang chờ giao hay chờ về trong mùa." />}
          <ListFooter list={list} />
        </>
      )}
      <p className="hint" style={{ marginTop: 8 }}>
        Lượng dự kiến về không được cộng thành tồn thực.
      </p>
    </>
  );
}
