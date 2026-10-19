import { useState } from "react";
import { matchesAllTokens } from "../../../shared/lib/textnorm";
import { Button } from "../../../shared/ui/button";
import { LOAD_MORE_LABEL, ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { invoicePayment, type PaymentCode } from "../api/adapters";
import { useCopyInvoice } from "../api/invoice-mutations";
import { useCustomerDirectory, useInvoiceList } from "../api/queries";
import { InvoiceTable } from "../components/invoice-table";
import { SalesOverlayHost } from "../containers/sales-overlay-host";
import { useSalesOverlay } from "../hooks/use-sales-overlay";
import type { Invoice } from "../schemas";

type StatusFilter = "all" | "draft" | "open" | "done";
type PaymentFilter = "all" | "unpaid" | "partial" | "paid";

const STATUS_MATCH: Readonly<Record<Exclude<StatusFilter, "all">, readonly Invoice["status"][]>> = {
  draft: ["draft"],
  open: ["confirmed", "partially_delivered"],
  done: ["completed", "cancelled_remaining"],
};

const PAYMENT_MATCH: Readonly<Record<Exclude<PaymentFilter, "all">, readonly PaymentCode[]>> = {
  unpaid: ["unpaid"],
  partial: ["partial"],
  paid: ["paid", "overpaid"],
};

function InvoicesView() {
  const overlay = useSalesOverlay();
  const list = useInvoiceList();
  const customers = useCustomerDirectory();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [payment, setPayment] = useState<PaymentFilter>("all");
  const [copyingId, setCopyingId] = useState<string | null>(null);
  const copy = useCopyInvoice((c) => overlay.openDrawer({ kind: "invoice", id: c.id }));

  const shown = list.items.filter((o) => {
    if (status !== "all" && !STATUS_MATCH[status].includes(o.status)) return false;
    if (payment !== "all" && !PAYMENT_MATCH[payment].includes(invoicePayment(o).code)) return false;
    return matchesAllTokens(q, o.doc_no, customers.name(o.customer_party_id));
  });

  const runCopy = async (o: Invoice) => {
    setCopyingId(o.id);
    try {
      await copy.run(o);
    } finally {
      setCopyingId(null);
    }
  };

  return (
    <>
      <PageHeader
        lead="Hóa đơn bán sỉ và quà doanh nghiệp. Nhập số tiền khách đã trả, hệ thống tự tính số còn lại và trạng thái thanh toán. Hóa đơn nháp được sửa, xóa; đã xác nhận bán thì khóa, chỉ sao chép. Đây là chứng từ nội bộ, không phải hóa đơn điện tử."
        actions={
          <Button variant="primary" onClick={() => overlay.openDialog({ kind: "invoiceForm", invoice: null })}>
            Tạo hóa đơn
          </Button>
        }
      />
      <div className="filters">
        <input type="search" value={q} placeholder="Tìm số hóa đơn, khách hàng…" aria-label="Tìm hóa đơn" onChange={(e) => setQ(e.target.value)} />
        <ToggleGroup<StatusFilter>
          label="Lọc theo trạng thái"
          value={status}
          onChange={setStatus}
          options={[
            { value: "all", label: "Tất cả" },
            { value: "draft", label: "Nháp" },
            { value: "open", label: "Chưa xuất đủ" },
            { value: "done", label: "Hoàn tất" },
          ]}
        />
        <span className="hint">Thanh toán:</span>
        <ToggleGroup<PaymentFilter>
          label="Lọc theo thanh toán"
          value={payment}
          onChange={setPayment}
          options={[
            { value: "all", label: "Tất cả" },
            { value: "unpaid", label: "Chưa thanh toán" },
            { value: "partial", label: "Một phần" },
            { value: "paid", label: "Đã đủ" },
          ]}
        />
      </div>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <InvoiceTable
          invoices={shown}
          customerName={customers.name}
          copyingId={copyingId}
          onEdit={(o) => overlay.openDialog({ kind: "invoiceForm", invoice: o })}
          onDelete={(o) => overlay.openDialog({ kind: "invoiceDelete", invoice: o })}
          onCopy={(o) => void runCopy(o)}
        />
      )}
      {/* The list has no payment filter and one status per request, so both filters and the footer cover the loaded rows. */}
      {list.hasMore ? <p className="hint">Bộ lọc và dòng tổng chỉ tính trên các hóa đơn đã tải; bấm “{LOAD_MORE_LABEL}” để tính tiếp.</p> : null}
      <ListFooter list={list} />
    </>
  );
}

/** Hóa đơn (/sales/invoices). */
export default function InvoicesPage() {
  return (
    <SalesOverlayHost>
      <InvoicesView />
    </SalesOverlayHost>
  );
}
