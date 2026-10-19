import { useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../../shared/ui/button";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useOpenOrders, useReceiptIdByDoc, useReceiptList } from "../api/queries";
import { DocNotFoundDrawer } from "../components/doc-not-found-drawer";
import { ReceiptDrawer } from "../components/receipt-drawer";
import { ReceiptTable } from "../components/receipt-table";
import { NewPurchaseReturnDialog } from "../containers/new-purchase-return-dialog";
import { NewReceiptDialog } from "../containers/new-receipt-dialog";
import { ReceiptInvoiceDialog } from "../containers/receipt-invoice-dialog";
import type { Order, ReceiptListItem } from "../schemas";

type Opened = { kind: "receive"; order: Order | null } | { kind: "invoice"; receipt: ReceiptListItem } | { kind: "return"; receiptId: string } | null;

/** Nhận hàng (/purchasing/receipts). `?doc=PN-…` opens a receipt. */
export default function ReceiptsPage() {
  const [params, setParams] = useSearchParams();
  // A row opened by click; a later `?doc=` link (another number) takes over the drawer.
  const [clicked, setClicked] = useState<{ id: string; doc: string | null } | null>(null);
  const [opened, setOpened] = useState<Opened>(null);
  const list = useReceiptList("");
  const openOrders = useOpenOrders();

  const doc = params.get("doc");
  const openId = clicked !== null && clicked.doc === doc ? clicked.id : null;
  const byDoc = useReceiptIdByDoc(openId === null ? doc : null);
  const drawerId = openId ?? (doc !== null ? (byDoc.data ?? null) : null);

  const dropDoc = () =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        next.delete("doc");
        return next;
      },
      { replace: true },
    );
  const openReceipt = (r: ReceiptListItem) => {
    setClicked({ id: r.id, doc: null });
    if (doc !== null) dropDoc();
  };
  const closeDrawer = () => {
    setClicked(null);
    if (doc !== null) dropDoc();
  };

  return (
    <>
      <PageHeader
        lead="Phiếu nhận ghi sổ làm tăng tồn và phát sinh nợ NCC theo giá nhận. Nhập nhanh không cần đơn và tạo được hàng hóa mới. Gắn hóa đơn NCC sau chỉ đối chiếu, không ghi lại nợ."
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "receive", order: null })}>
            Nhập nhanh
          </Button>
        }
      />
      <FilterBar>
        {(openOrders.data ?? []).map((o) => (
          <Button key={o.id} onClick={() => setOpened({ kind: "receive", order: o })}>
            Nhận theo {o.doc_no} · {o.supplier_name}
          </Button>
        ))}
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <ReceiptTable rows={list.items} onOpen={openReceipt} onMatchInvoice={(receipt) => setOpened({ kind: "invoice", receipt })} />
      )}
      <ListFooter list={list} />
      {drawerId !== null ? (
        <ReceiptDrawer id={drawerId} onClose={closeDrawer} onReturn={(r) => setOpened({ kind: "return", receiptId: r.id })} />
      ) : doc !== null && (byDoc.isSuccess || byDoc.isError) ? (
        <DocNotFoundDrawer title="Phiếu nhận" docNo={doc} error={byDoc.error} onClose={closeDrawer} />
      ) : null}
      {opened?.kind === "receive" ? <NewReceiptDialog order={opened.order} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "invoice" ? <ReceiptInvoiceDialog receipt={opened.receipt} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "return" ? <NewPurchaseReturnDialog receiptId={opened.receiptId} onClose={() => setOpened(null)} /> : null}
    </>
  );
}
