import { useState } from "react";
import { useNavigate } from "react-router";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { dec } from "../../../shared/lib/decimal";
import { fd, money, num } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { useSupplierStatus } from "../api/mutations";
import { useSupplierList } from "../api/queries";
import { SupplierDeleteDialog } from "../containers/supplier-delete-dialog";
import { SupplierDialog } from "../containers/supplier-dialog";
import { SupplierStopDialog } from "../containers/supplier-stop-dialog";
import type { SupplierListItem } from "../schemas";
import { SUPPLIER_ERROR_MESSAGES } from "../supplier-form-values";

type StatusChip = "on" | "off" | "all";
type Opened = { kind: "edit"; supplier: SupplierListItem | null } | { kind: "delete" | "stop"; supplier: SupplierListItem } | null;

const STOPPED = "Ngừng giao dịch";
const stopReasonId = (s: SupplierListItem) => `supplier-stop-reason-${s.id}`;
const ACTIVE = "Đang giao dịch";

/** Nhà cung cấp (/catalog/suppliers). */
export default function SuppliersPage() {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [chip, setChip] = useState<StatusChip>("on");
  const [opened, setOpened] = useState<Opened>(null);
  const list = useSupplierList(useDebounce(q.trim(), 250));
  const status = useSupplierStatus();
  // The supplier whose "Mở lại" is in flight: only its button shows the wait.
  const [reopening, setReopening] = useState<string | null>(null);

  const active = list.items.filter((s) => s.status === "active");
  const stopped = list.items.filter((s) => s.status === "inactive");
  const rows = chip === "all" ? list.items : chip === "on" ? active : stopped;

  const columns: Column<SupplierListItem>[] = [
    {
      key: "name",
      header: "Nhà cung cấp",
      cell: (s) => (
        <>
          <b style={{ color: "var(--navy-900)" }}>{s.name}</b>
          {s.is_customer ? (
            <>
              {" "}
              <Badge alt>kiêm khách hàng</Badge>
            </>
          ) : null}
          <CellSub>{s.tax_code ? `MST ${s.tax_code}` : ""}</CellSub>
        </>
      ),
    },
    {
      key: "contact",
      header: "Liên hệ",
      cell: (s) => (
        <>
          {s.contact_name ?? "—"}
          <CellSub>{s.phone ?? ""}</CellSub>
          <CellSub>{s.address ?? ""}</CellSub>
        </>
      ),
    },
    { key: "goods", header: "Mặt hàng cung cấp", cell: (s) => s.supplied_goods_note ?? "—" },
    { key: "terms", header: "Điều khoản", cell: (s) => (s.payment_terms_days > 0 ? `Nợ ${s.payment_terms_days} ngày` : "Trả ngay") },
    {
      key: "orders",
      header: "Đơn nhập",
      numeric: true,
      cell: (s) => (
        <>
          {num(s.order_count)}
          <CellSub>nhập gần nhất {fd(s.last_receipt_date)}</CellSub>
        </>
      ),
    },
    { key: "payable", header: "Cửa hàng còn nợ", numeric: true, cell: (s) => (dec(s.payable_open).isZero() ? money(0) : <b>{money(s.payable_open)}</b>) },
    { key: "status", header: "Trạng thái", cell: (s) => <StatusPill label={s.status === "inactive" ? STOPPED : ACTIVE} /> },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (s) => (
        <ButtonRow style={{ flexWrap: "nowrap", whiteSpace: "nowrap" }}>
          <Button variant="navy" size="sm" aria-label={`Sửa ${s.name}`} onClick={() => setOpened({ kind: "edit", supplier: s })}>
            Sửa
          </Button>
          {s.status === "inactive" ? (
            <Button
              size="sm"
              aria-label={`Mở lại ${s.name}`}
              // One write at a time: the other rows wait, only this one says so.
              disabled={status.pending}
              pending={status.pending && reopening === s.id}
              onClick={() => {
                setReopening(s.id);
                void status.run({ supplier: s, on: true });
              }}
            >
              Mở lại
            </Button>
          ) : (
            <>
              <Button size="sm" aria-label={`Tạo đơn nhập ${s.name}`} onClick={() => void navigate(`/purchasing/orders?supplier=${encodeURIComponent(s.id)}`)}>
                Tạo đơn nhập
              </Button>
              {/* Never disabled: focus and touch reach it, the description says why it cannot be stopped yet and the dialog repeats it. */}
              <Button size="sm" aria-label={`Ngừng ${s.name}`} aria-describedby={s.can_stop ? undefined : stopReasonId(s)} onClick={() => setOpened({ kind: "stop", supplier: s })}>
                Ngừng
              </Button>
              {s.can_stop ? null : (
                <span id={stopReasonId(s)} className="sr-only">
                  {SUPPLIER_ERROR_MESSAGES.supplier_has_open_orders}
                </span>
              )}
            </>
          )}
          {s.can_delete ? (
            <Button variant="danger" size="sm" aria-label={`Xóa ${s.name}`} onClick={() => setOpened({ kind: "delete", supplier: s })}>
              Xóa
            </Button>
          ) : null}
        </ButtonRow>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        lead="Danh sách dùng khi tạo đơn nhập và nhập nhanh. Nhà cung cấp ngừng giao dịch được ẩn khỏi ô chọn nhưng vẫn giữ lịch sử và vẫn trả nợ được. Chỉ xóa được NCC chưa có chứng từ."
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "edit", supplier: null })}>
            Thêm nhà cung cấp
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder="Tìm tên, mã, số điện thoại, người liên hệ, mặt hàng…" />
        <ToggleGroup
          label="Trạng thái giao dịch"
          value={chip}
          onChange={setChip}
          options={[
            { value: "on", label: `${ACTIVE} (${active.length})` },
            { value: "off", label: `${STOPPED} (${stopped.length})` },
            { value: "all", label: "Tất cả" },
          ]}
        />
      </FilterBar>
      {list.isLoading ? <ListLoading /> : <DataTable caption="Nhà cung cấp" columns={columns} rows={rows} rowKey={(s) => s.id} empty="Chưa có nhà cung cấp phù hợp." />}
      <ListFooter list={list} />
      {opened?.kind === "edit" ? <SupplierDialog supplier={opened.supplier} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "delete" ? <SupplierDeleteDialog supplier={opened.supplier} onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "stop" ? <SupplierStopDialog supplier={opened.supplier} onClose={() => setOpened(null)} /> : null}
    </>
  );
}
