import { useState } from "react";
import { useNavigate } from "react-router";
import { useServerBusinessDate } from "../../../shared/api/platform-queries";
import { resolveDocOpener } from "../../../shared/domain/doc-no";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { businessDateOf } from "../../../shared/lib/dates";
import { fd, money } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useReorder } from "../api/mutations";
import { useCustomers, useRevenueByCustomer } from "../api/queries";
import { CustomerDialog } from "../containers/customer-dialog";
import { marketingChannels, revenueRefusalMessage } from "../customer-form-values";
import type { Customer, CustomerListItem } from "../schemas";

const NONE = "—";
const INVOICES_ROUTE = "/sales/invoices";

type Opened = { customer: Customer | null } | null;

/** Khách hàng (CRM) (/campaigns/customers). */
export default function CustomersPage() {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [opened, setOpened] = useState<Opened>(null);
  const list = useCustomers(useDebounce(q.trim(), 250));
  const revenue = useRevenueByCustomer(
    useServerBusinessDate(),
    list.items.map((c) => c.id),
  );
  const revenueRefusal = revenueRefusalMessage(revenue.error);
  // The row whose reorder is running; the other rows wait for it.
  const [reordering, setReordering] = useState<string | null>(null);
  const reorder = useReorder((r) => {
    const open = resolveDocOpener(r.invoice_doc_no);
    if (open) open(r.invoice_doc_no);
    else void navigate(INVOICES_ROUTE);
  });

  const columns: Column<CustomerListItem>[] = [
    { key: "name", header: "Khách", cell: (c) => <CellText main={c.name} sub={[c.phone, c.address].filter(Boolean).join(" · ")} /> },
    { key: "group", header: "Nhóm", cell: (c) => <Badge>{c.customer_group_name}</Badge> },
    {
      key: "revenue",
      header: "Doanh thu từ 01/10",
      numeric: true,
      cell: (c) => {
        const net = revenue.byCustomer.get(c.id);
        return net === undefined ? NONE : money(net);
      },
    },
    { key: "last", header: "Lần mua gần nhất", cell: (c) => (c.last_purchase_at ? fd(businessDateOf(c.last_purchase_at)) : NONE) },
    { key: "consent", header: "Đồng ý nhận tin", cell: (c) => <StatusPill label={marketingChannels(c.consents).length > 0 ? "Đã đồng ý" : "Chưa đồng ý"} /> },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (c) => (
        <ButtonRow style={{ flexWrap: "nowrap", whiteSpace: "nowrap" }}>
          {c.status === "active" ? (
            <OrderButton
              customer={c}
              busy={reorder.pending && reordering === c.id}
              locked={reorder.pending}
              onReorder={() => {
                setReordering(c.id);
                void reorder.run(c);
              }}
              onNew={() => void navigate(INVOICES_ROUTE)}
            />
          ) : null}
          <Button size="sm" aria-label={`Sửa ${c.name}`} onClick={() => setOpened({ customer: c })}>
            Sửa
          </Button>
        </ButtonRow>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        lead="Lịch sử mua, người phụ trách, đồng ý nhận tin. Tái đặt đơn lấy giá và quy cách hiện hành, cần xác nhận lại, không sao chép mù đơn cũ."
        actions={
          <Button variant="primary" onClick={() => setOpened({ customer: null })}>
            Thêm khách hàng
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder="Tìm tên, điện thoại, mã số thuế…" />
      </FilterBar>
      {list.isLoading ? <ListLoading /> : <DataTable caption="Khách hàng" columns={columns} rows={list.items} rowKey={(c) => c.id} empty="Chưa có khách hàng phù hợp." />}
      {revenueRefusal !== null ? <p className="hint">{revenueRefusal}</p> : null}
      <ListFooter list={list} />
      {opened !== null ? <CustomerDialog customer={opened.customer} onClose={() => setOpened(null)} /> : null}
    </>
  );
}

/**
 * "Tái đặt đơn" for a customer who has bought before, "Tạo đơn" otherwise. `busy`: this customer's
 * reorder is running; `locked`: a reorder of any row is.
 */
function OrderButton({ customer, busy, locked, onReorder, onNew }: { customer: CustomerListItem; busy: boolean; locked: boolean; onReorder: () => void; onNew: () => void }) {
  if (customer.last_purchase_at !== null) {
    return (
      <Button variant="navy" size="sm" pending={busy} pendingLabel="Đang tạo đơn…" disabled={locked} aria-label={`Tái đặt đơn ${customer.name}`} onClick={onReorder}>
        Tái đặt đơn
      </Button>
    );
  }
  return (
    <Button variant="navy" size="sm" aria-label={`Tạo đơn ${customer.name}`} onClick={onNew}>
      Tạo đơn
    </Button>
  );
}
