import { useState } from "react";
import { Button } from "../../../shared/ui/button";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useColorDirectory } from "../../catalog";
import { useAdjustmentList, useReasons } from "../api/queries";
import { AdjustmentTable } from "../components/adjustment-table";
import { AdjustmentDialog } from "../containers/adjustment-dialog";
import { useUrlFilters } from "../use-url-filters";

/** Điều chỉnh kho (/inventory/adjustments). `?doc=DC-…` asks the server for that adjustment alone. */
export default function AdjustmentsPage() {
  const url = useUrlFilters();
  const doc = url.get("doc");
  const list = useAdjustmentList(doc);
  const reasons = useReasons();
  const colors = useColorDirectory();
  const [creating, setCreating] = useState(false);
  const reasonName = (id: string | null | undefined) => reasons.data?.find((r) => r.id === id)?.name ?? null;

  return (
    <>
      <PageHeader
        lead="Hỏng, hao hụt, biếu tặng, dùng nội bộ, tiêu hủy. Bắt buộc loại và lý do; xuất không bán không tạo doanh thu."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Tạo điều chỉnh
          </Button>
        }
      />
      {doc !== null ? (
        <FilterBar>
          <span className="hint">Phiếu {doc}</span>
          <Button size="sm" onClick={() => url.set({ doc: null })}>
            Xem tất cả
          </Button>
        </FilterBar>
      ) : null}
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <AdjustmentTable rows={list.items} reasonName={reasonName} colorOf={colors.codeOf} empty={doc === null ? "Chưa có điều chỉnh." : `Không tìm thấy ${doc}.`} />
      )}
      <ListFooter list={list} />
      {creating ? <AdjustmentDialog onClose={() => setCreating(false)} /> : null}
    </>
  );
}
