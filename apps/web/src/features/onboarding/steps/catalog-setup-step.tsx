import { useState } from "react";
import { vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { TextLink } from "../../../shared/ui/link-button";
import { ListLoading } from "../../../shared/ui/list-footer";
import { ScreenError } from "../../../app/route-fallbacks";
import { QuickProductDialog, baseUnitName, useSkuList, type SkuListItem } from "../../catalog";
import { useStepCommand } from "../api/mutations";
import { ACTIVE_SKUS } from "../api/queries";
import { OnbEmpty } from "../components/opening-gate";
import { useOnboardingScreen } from "../onboarding-context";
import { nextStep } from "../steps";

const COLUMNS: ReadonlyArray<Column<SkuListItem>> = [
  { key: "code", header: "Mã", cell: (s) => <span className="doc">{s.code}</span> },
  { key: "name", header: "Hàng hóa", cell: (s) => <CellText main={s.display_name} sub={s.variant_label} /> },
  { key: "unit", header: "Đơn vị", cell: (s) => baseUnitName(s) },
  { key: "lot", header: "Theo lô", cell: (s) => (s.track_lot ? "Có" : "Không") },
  { key: "price", header: "Giá bán", numeric: true, cell: (s) => vnd(s.retail_price) },
];

/** Prototype `OB_BODY.catalog_setup`: the main products, added one by one through the quick form. */
export function CatalogSetupStep() {
  const { steps, cta, goTo } = useOnboardingScreen();
  const [adding, setAdding] = useState(false);
  const list = useSkuList(ACTIVE_SKUS);
  const done = useStepCommand("catalog_setup", "complete", { after: () => goTo(nextStep("catalog_setup")) });
  const completed = steps.find((s) => s.key === "catalog_setup")?.status === "completed";
  if (list.isLoading) return <ListLoading />;
  if (list.error && list.items.length === 0) return <ScreenError error={list.error} onRetry={() => void list.refetch()} />;
  const any = list.items.length > 0;
  return (
    <>
      {any ? (
        <DataTable caption="Hàng hóa" columns={COLUMNS} rows={list.items} rowKey={(s) => s.id} flat />
      ) : (
        <OnbEmpty title="Chưa có hàng hóa nào" text="Thêm vài mặt hàng chính bây giờ để nhập được tồn kho đầu kỳ." />
      )}
      <ButtonRow>
        <Button variant={any ? "line" : cta} onClick={() => setAdding(true)}>
          + Thêm hàng hóa
        </Button>
        {any && !completed ? (
          <Button variant={cta} pending={done.pending} onClick={() => void done.run()}>
            Xong, sang tồn kho
          </Button>
        ) : null}
        <TextLink to="/catalog/products">Mở danh mục đầy đủ</TextLink>
      </ButtonRow>
      {adding ? <QuickProductDialog onClose={() => setAdding(false)} /> : null}
    </>
  );
}
