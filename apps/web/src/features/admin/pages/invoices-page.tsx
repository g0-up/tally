import { useState } from "react";
import { Link } from "react-router";
import { fdt } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { MoneyText } from "../../../shared/ui/money-text";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { useInvoices, type Filter } from "../api/queries";
import type { AdminInvoice, InvoiceStatus } from "../api/schemas";
import { PagedRows } from "../components/paged-rows";
import { TenantPicker, type PickedTenant } from "../components/tenant-picker";
import { IssueInvoiceDialog, MarkPaidDialog, VoidInvoiceDialog } from "../containers/invoice-dialogs";
import { INVOICE_PURPOSE, INVOICE_STATUS, entriesOf } from "../lib/labels";

export const INVOICES_TITLE = "Hóa đơn thuê bao";
export const INVOICES_TABLE = "Danh sách hóa đơn thuê bao";

const STATUS_OPTIONS: { value: Filter<InvoiceStatus>; label: string }[] = [{ value: "", label: "Tất cả" }, ...entriesOf(INVOICE_STATUS).map(([value, s]) => ({ value, label: s.label }))];

type Open = { kind: "issue" } | { kind: "paid" | "void"; invoice: AdminInvoice } | null;

/** Hóa đơn thuê bao (/admin/invoices): issue, mark paid by bank reference, void. */
export default function InvoicesPage() {
  const [status, setStatus] = useState<Filter<InvoiceStatus>>("");
  const [tenant, setTenant] = useState<PickedTenant | null>(null);
  const [open, setOpen] = useState<Open>(null);
  const invoices = useInvoices(status, tenant?.id ?? "");
  const close = () => setOpen(null);

  const columns: Column<AdminInvoice>[] = [
    {
      key: "number",
      header: "Số hóa đơn",
      cell: (i) => (
        <>
          <b>{i.number}</b>
          <CellSub>{INVOICE_PURPOSE[i.purpose]}</CellSub>
        </>
      ),
    },
    {
      key: "store",
      header: "Cửa hàng",
      cell: (i) => (
        <>
          <Link to={`/admin/tenants/${i.tenant_id}`}>{i.tenant_name}</Link>
          <CellSub>{i.tenant_slug}</CellSub>
        </>
      ),
    },
    { key: "period", header: "Kỳ", cell: (i) => `${fd(i.period_start)} – ${fd(i.period_end)}` },
    { key: "amount", header: "Số tiền", numeric: true, cell: (i) => <MoneyText value={i.amount} /> },
    {
      key: "status",
      header: "Trạng thái",
      cell: (i) => (
        <>
          <StatusPill {...INVOICE_STATUS[i.status]} />
          {i.paid_at ? <CellSub>{`${fdt(i.paid_at)}${i.provider_ref ? ` · ${i.provider_ref}` : ""}`}</CellSub> : null}
        </>
      ),
    },
    { key: "issued", header: "Lập lúc", cell: (i) => fdt(i.issued_at) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (i) =>
        i.status === "open" ? (
          <ButtonRow>
            <Button size="sm" aria-label={`Ghi nhận thanh toán ${i.number}`} onClick={() => setOpen({ kind: "paid", invoice: i })}>
              Đã thanh toán
            </Button>
            <Button size="sm" variant="danger" aria-label={`Hủy ${i.number}`} onClick={() => setOpen({ kind: "void", invoice: i })}>
              Hủy
            </Button>
          </ButtonRow>
        ) : null,
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Quản trị nền tảng"
        title={INVOICES_TITLE}
        actions={
          <Button variant="primary" onClick={() => setOpen({ kind: "issue" })}>
            Lập hóa đơn
          </Button>
        }
      />
      <FilterBar>
        <ToggleGroup label="Trạng thái hóa đơn" options={STATUS_OPTIONS} value={status} onChange={setStatus} />
        <TenantPicker aria-label="Cửa hàng" noneLabel="Mọi cửa hàng" value={tenant} onChange={setTenant} />
      </FilterBar>
      <PagedRows list={invoices} what="hóa đơn thuê bao">
        <DataTable caption={INVOICES_TABLE} columns={columns} rows={invoices.items} rowKey={(i) => i.id} empty="Không có hóa đơn khớp bộ lọc." />
      </PagedRows>
      {open?.kind === "issue" ? <IssueInvoiceDialog onClose={close} /> : null}
      {open?.kind === "paid" ? <MarkPaidDialog invoice={open.invoice} onClose={close} /> : null}
      {open?.kind === "void" ? <VoidInvoiceDialog invoice={open.invoice} onClose={close} /> : null}
    </>
  );
}
