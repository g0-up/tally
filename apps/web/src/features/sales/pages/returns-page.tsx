import { Button } from "../../../shared/ui/button";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useCustomerDirectory, useExchangeList, useReasons, useSalesDocumentList, useSalesReturnList } from "../api/queries";
import { ExchangeTable } from "../components/exchange-table";
import { ReturnTable } from "../components/return-table";
import { SalesOverlayHost } from "../containers/sales-overlay-host";
import { useSalesOverlay } from "../hooks/use-sales-overlay";

function ReturnsView() {
  const overlay = useSalesOverlay();
  const returns = useSalesReturnList();
  const exchanges = useExchangeList();
  const documents = useSalesDocumentList();
  const customers = useCustomerDirectory();
  const sourced = useReasons("sales_return");
  const unsourced = useReasons("return_without_source");

  const docNos = new Map(documents.items.map((d) => [d.id, d.doc_no]));
  const returnNos = new Map(returns.items.map((r) => [r.id, r.doc_no]));
  const reasonNames = new Map([...(sourced.data ?? []), ...(unsourced.data ?? [])].map((r) => [r.id, r.name]));
  const docNoOf = (id: string | null) => (id ? (docNos.get(id) ?? null) : null);

  return (
    <>
      <PageHeader
        lead="Trả theo chứng từ bán gốc (hóa đơn đã giao hoặc bán tại quầy), không vượt lượng đã giao. Hàng trả vào khu cách ly chờ kiểm tra; giảm nợ trước, phần vượt nợ thành khoản cần hoàn."
        actions={
          <>
            <Button onClick={() => overlay.openDialog({ kind: "exchange", documentId: null })}>Đổi hàng</Button>
            <Button variant="primary" onClick={() => overlay.openDialog({ kind: "return", documentId: null })}>
              Tạo phiếu trả
            </Button>
          </>
        }
      />
      {returns.isLoading ? (
        <ListLoading />
      ) : (
        <ReturnTable
          returns={returns.items}
          customerName={customers.name}
          docNoOf={docNoOf}
          reasonName={(id) => reasonNames.get(id) ?? "—"}
          onRefund={(r) => overlay.openDialog({ kind: "refund", salesReturn: r })}
        />
      )}
      <ListFooter list={returns} />
      <h3 className="sec-h">Phiếu đổi hàng</h3>
      {exchanges.isLoading ? (
        <ListLoading />
      ) : (
        <ExchangeTable exchanges={exchanges.items} customerName={customers.name} docNoOf={docNoOf} returnNoOf={(id) => returnNos.get(id) ?? null} />
      )}
      <ListFooter list={exchanges} />
    </>
  );
}

/** Trả hàng (/sales/returns). */
export default function ReturnsPage() {
  return (
    <SalesOverlayHost>
      <ReturnsView />
    </SalesOverlayHost>
  );
}
