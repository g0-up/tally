import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../../shared/ui/button";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useSeenIds } from "../../inventory";
import { useAssemblies, useDisassemblyList } from "../api/queries";
import { DisassemblyTable } from "../components/disassembly-table";
import { DisassemblyDialog } from "../containers/disassembly-dialog";

/** Tháo giỏ (/gift-baskets/disassembly). `?doc=TG-…` searches the server for that disassembly. */
export default function DisassemblyPage() {
  const [params, setParams] = useSearchParams();
  const doc = params.get("doc");
  // The server search also matches order and lot numbers and kit names, so the number is kept exact here.
  const list = useDisassemblyList(doc ?? "");
  const rows = useMemo(() => (doc === null ? list.items : list.items.filter((d) => d.doc_no === doc)), [doc, list.items]);
  const { seen, markSeen } = useSeenIds();
  const orderIds = useMemo(() => [...new Set(rows.flatMap((d) => (seen.has(d.id) ? [d.order_id] : [])))], [rows, seen]);
  const orders = useAssemblies(orderIds);
  const [creating, setCreating] = useState(false);

  return (
    <>
      <PageHeader
        lead="Tháo giỏ dư trả thành phần về kho theo lô đã dùng. Vật tư bao bì hỏng không tự trở lại tồn bán được."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Tháo giỏ
          </Button>
        }
      />
      {doc !== null ? (
        <FilterBar>
          <span className="hint">Phiếu {doc}</span>
          <Button size="sm" onClick={() => setParams({}, { replace: true })}>
            Xem tất cả
          </Button>
        </FilterBar>
      ) : null}
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <DisassemblyTable rows={rows} kitName={(id) => orders.get(id)?.kit_name} onSeen={markSeen} empty={doc === null ? "Chưa có phiếu tháo giỏ." : `Không tìm thấy ${doc}.`} />
      )}
      <ListFooter list={list} />
      {creating ? <DisassemblyDialog onClose={() => setCreating(false)} /> : null}
    </>
  );
}
