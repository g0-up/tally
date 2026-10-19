import { useState } from "react";
import { useNavigate } from "react-router";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { lotLine, useCreateAdjustment, usePerRowCommit } from "../api/mutations";
import { useLotList, type LotFilter } from "../api/queries";
import { LotTable } from "../components/lot-table";
import { DisposeLotDialog } from "../containers/dispose-lot-dialog";
import type { Lot } from "../schemas";
import { useUrlFilters } from "../use-url-filters";

const FILTERS: ReadonlyArray<{ value: LotFilter; label: string }> = [
  { value: "all", label: "Tất cả lô" },
  { value: "soon", label: "Hết hạn trong 90 ngày" },
  { value: "expired", label: "Đã hết hạn" },
  { value: "quarantine", label: "Khu cách ly" },
];

const toFilter = (v: string | null): LotFilter => FILTERS.find((f) => f.value === v)?.value ?? "all";

/** Lô & hạn dùng (/inventory/lots). The chip lives in the URL as `loc`. */
export default function LotsPage() {
  const url = useUrlFilters();
  const filter = toFilter(url.get("loc"));
  const list = useLotList(filter);
  const navigate = useNavigate();
  const [disposing, setDisposing] = useState<Lot | null>(null);
  const pass = usePerRowCommit(
    useCreateAdjustment(() => "Hàng đã trở lại tồn bán được.", () => undefined),
    (v) => v.lines[0]?.lot_id ?? "",
  );
  const reject = usePerRowCommit(
    useCreateAdjustment(() => "Đã ghi tiêu hủy hàng cách ly.", () => undefined),
    (v) => v.lines[0]?.lot_id ?? "",
  );

  return (
    <>
      <PageHeader lead="Soạn hàng theo FEFO (hết hạn trước, xuất trước). FEFO là thứ tự lấy hàng, không phải phương pháp tính giá vốn. Lô hết hạn bị chặn bán mặc định." />
      <FilterBar>
        <ToggleGroup label="Lọc lô" options={FILTERS} value={filter} onChange={(v) => url.set({ loc: v === "all" ? null : v })} />
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <LotTable
          rows={list.items}
          busy={pass.pending || reject.pending}
          onPass={(l) => void pass.run({ kind: "quarantine_release", reasonId: null, note: "Đạt kiểm tra", lines: [lotLine(l)], name: l.sku_name })}
          onReject={(l) => void reject.run({ kind: "disposal", reasonId: null, note: "Không đạt kiểm tra", lines: [lotLine(l)], name: l.sku_name })}
          onDispose={setDisposing}
          onClearance={() => void navigate("/campaigns/seasons")}
        />
      )}
      <ListFooter list={list} />
      {disposing ? <DisposeLotDialog lot={disposing} onClose={() => setDisposing(null)} /> : null}
    </>
  );
}
