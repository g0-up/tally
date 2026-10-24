import { useState } from "react";
import { useSearchParams } from "react-router";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { Button } from "../../../shared/ui/button";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useConfirmOrder } from "../api/mutations";
import { useOrderIdByDoc, useOrderList, useSupplierRef } from "../api/queries";
import { DocNotFoundDrawer } from "../components/doc-not-found-drawer";
import { PoDrawer } from "../components/po-drawer";
import { PoTable } from "../components/po-table";
import { NewPoDialog } from "../containers/new-po-dialog";
import { NewReceiptDialog } from "../containers/new-receipt-dialog";
import { PoAdvanceDialog } from "../containers/po-advance-dialog";
import { PoCloseDialog } from "../containers/po-close-dialog";
import { PoDeleteDialog } from "../containers/po-delete-dialog";
import type { Order } from "../schemas";

type Opened =
  | { kind: "new" }
  | { kind: "edit"; order: Order }
  | { kind: "delete"; order: Order }
  | { kind: "receive"; order: Order } | { kind: "advance"; order: Order } | { kind: "close"; order: Order } | null;

/** Đơn nhập (/purchasing/orders). `?doc=PO-…` opens an order, `?supplier=<id>` a new order for that supplier. */
export default function OrdersPage() {
  const today = useBusinessDate();
  const [params, setParams] = useSearchParams();
  const [q, setQ] = useState("");
  // A row opened by click; a later `?doc=` link (another number) takes over the drawer.
  const [clicked, setClicked] = useState<{ id: string; doc: string | null } | null>(null);
  const [opened, setOpened] = useState<Opened>(null);
  const list = useOrderList(useDebounce(q.trim(), 250));
  const confirm = useConfirmOrder();

  const doc = params.get("doc");
  const openId = clicked !== null && clicked.doc === doc ? clicked.id : null;
  const byDoc = useOrderIdByDoc(openId === null ? doc : null);
  const drawerId = openId ?? (doc !== null ? (byDoc.data ?? null) : null);
  const supplierId = params.get("supplier");
  const supplier = useSupplierRef(supplierId);

  const dropParam = (name: string) =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        next.delete(name);
        return next;
      },
      { replace: true },
    );
  const openOrder = (o: Order) => {
    setClicked({ id: o.id, doc: null });
    if (doc !== null) dropParam("doc");
  };
  const closeDrawer = () => {
    setClicked(null);
    if (doc !== null) dropParam("doc");
  };

  return (
    <>
      <PageHeader
        lead="Đơn nhập chưa tạo tồn hay nợ NCC. Tồn và nợ chỉ phát sinh khi nhận hàng được ghi sổ; nhận được nhiều đợt. Có thể thêm hàng hóa mới ngay trong đơn và chỉnh giá bán lẻ."
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "new" })}>
            Tạo đơn nhập
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder="Tìm số đơn, nhà cung cấp…" />
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <PoTable
          rows={list.items}
          today={today}
          onOpen={openOrder}
          onConfirm={(o) => void confirm.run({ id: o.id, docNo: o.doc_no, version: o.version })}
          onEdit={(order) => setOpened({ kind: "edit", order })}
          onDelete={(order) => setOpened({ kind: "delete", order })}
          onReceive={(order) => setOpened({ kind: "receive", order })}
          confirming={confirm.pending}
        />
      )}
      <ListFooter list={list} />
      {drawerId !== null ? (
        <PoDrawer
          id={drawerId}
          onClose={closeDrawer}
          onReceive={(order) => setOpened({ kind: "receive", order })}
          onAdvance={(order) => setOpened({ kind: "advance", order })}
          onCloseShort={(order) => setOpened({ kind: "close", order })}
          onEdit={(order) => setOpened({ kind: "edit", order })}
          onDelete={(order) => setOpened({ kind: "delete", order })}
        />
      ) : doc !== null && (byDoc.isSuccess || byDoc.isError) ? (
        <DocNotFoundDrawer title="Đơn nhập" docNo={doc} error={byDoc.error} onClose={closeDrawer} />
      ) : null}
      {opened?.kind === "new" ? <NewPoDialog onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "edit" ? <NewPoDialog order={opened.order} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "delete" ? (
        <PoDeleteDialog
          order={opened.order}
          onClose={() => setOpened(null)}
          onDeleted={() => {
            // The drawer of the deleted draft has nothing left to show.
            if (drawerId === opened.order.id) closeDrawer();
            setOpened(null);
          }}
        />
      ) : null}
      {supplierId !== null && opened === null && (supplier.isSuccess || supplier.isError) ? (
        <NewPoDialog supplier={supplier.data && supplier.data.status === "active" ? { id: supplier.data.id, name: supplier.data.name } : null} onClose={() => dropParam("supplier")} />
      ) : null}
      {opened?.kind === "receive" ? <NewReceiptDialog order={opened.order} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "advance" ? <PoAdvanceDialog order={opened.order} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "close" ? <PoCloseDialog order={opened.order} onClose={() => setOpened(null)} /> : null}
    </>
  );
}
