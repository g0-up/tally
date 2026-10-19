import { useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../../shared/ui/button";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useReasons, useReturnByDoc, useReturnList } from "../api/queries";
import { DocNotFoundDrawer } from "../components/doc-not-found-drawer";
import { PurchaseReturnTable } from "../components/purchase-return-table";
import { NewPurchaseReturnDialog } from "../containers/new-purchase-return-dialog";

/**
 * Trả NCC (/purchasing/supplier-returns). A return has no drawer in the prototype, so `?doc=XT-…`
 * narrows the table to that return until "Tất cả" is pressed.
 */
export default function SupplierReturnsPage() {
  const [params, setParams] = useSearchParams();
  const [creating, setCreating] = useState(false);
  const list = useReturnList();
  const reasons = useReasons("purchase_return");
  const doc = params.get("doc");
  const one = useReturnByDoc(doc);

  const reasonName = (id: string | null | undefined) => reasons.data?.find((r) => r.id === id)?.name ?? "—";
  const showAll = () =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        next.delete("doc");
        return next;
      },
      { replace: true },
    );

  return (
    <>
      <PageHeader
        lead="Xuất trả đúng lô đã nhận; giảm nợ NCC. Nếu đã trả tiền nhiều hơn số còn nợ, phần chênh ghi thành khoản NCC cần hoàn."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Tạo phiếu trả NCC
          </Button>
        }
      />
      {doc === null ? (
        <>
          {list.isLoading ? <ListLoading /> : <PurchaseReturnTable rows={list.items} reasonName={reasonName} />}
          <ListFooter list={list} />
        </>
      ) : (
        <>
          <FilterBar>
            <Button size="sm" onClick={showAll}>
              Tất cả
            </Button>
          </FilterBar>
          {one.isPending ? <ListLoading /> : <PurchaseReturnTable rows={one.data ? [one.data] : []} reasonName={reasonName} />}
          {one.isError || (one.isSuccess && one.data === null) ? <DocNotFoundDrawer title="Phiếu xuất trả NCC" docNo={doc} error={one.error} onClose={showAll} /> : null}
        </>
      )}
      {creating ? <NewPurchaseReturnDialog onClose={() => setCreating(false)} /> : null}
    </>
  );
}
