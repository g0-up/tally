import { useState } from "react";
import { Link } from "react-router";
import { addDays, diffDays, todayBusinessDate } from "../../../shared/lib/dates";
import { fd, num } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { PageHeader } from "../../../shared/ui/page-header";
import { useUsage } from "../api/queries";
import type { UsageRow } from "../api/schemas";
import { PagedRows } from "../components/paged-rows";
import { storageText } from "../lib/store-url";

export const USAGE_TITLE = "Mức sử dụng";
export const USAGE_TABLE = "Mức sử dụng theo ngày";
const MAX_RANGE_DAYS = 366;

/** The range the API accepts, or the reason it does not. */
function rangeError(from: string | null, to: string | null): string | null {
  if (!from || !to) return "Chọn đủ ngày bắt đầu và ngày kết thúc.";
  const days = diffDays(to, from);
  if (days < 0) return "Ngày bắt đầu phải trước hoặc bằng ngày kết thúc.";
  if (days >= MAX_RANGE_DAYS) return `Khoảng ngày tối đa ${MAX_RANGE_DAYS} ngày.`;
  return null;
}

/** Mức sử dụng (/admin/usage): daily counters of every store, last 30 days by default. */
export default function UsagePage() {
  // The device's clock on purpose: the admin /me sends no server time, and this date only seeds the
  // default range of a report the admin can change; nothing is written with it.
  const today = todayBusinessDate();
  const [from, setFrom] = useState<string | null>(addDays(today, -29));
  const [to, setTo] = useState<string | null>(today);
  const error = rangeError(from, to);
  const usage = useUsage(from ?? "", to ?? "", error === null);

  const columns: Column<UsageRow>[] = [
    {
      key: "store",
      header: "Cửa hàng",
      cell: (u) => (
        <>
          <Link to={`/admin/tenants/${u.tenant_id}`}>{u.tenant_name}</Link>
          <CellSub>{u.slug}</CellSub>
        </>
      ),
    },
    { key: "date", header: "Ngày", cell: (u) => fd(u.usage_date) },
    { key: "skus", header: "Mã hàng đang bán", numeric: true, cell: (u) => num(u.active_skus) },
    { key: "docs", header: "Chứng từ đã ghi sổ", numeric: true, cell: (u) => num(u.documents_posted) },
    { key: "storage", header: "Dung lượng", numeric: true, cell: (u) => storageText(u.storage_bytes) },
  ];

  return (
    <>
      <PageHeader eyebrow="Quản trị nền tảng" title={USAGE_TITLE} lead="Số liệu theo ngày của mọi cửa hàng, tối đa 366 ngày mỗi lần xem." />
      <FilterBar>
        <Field label="Từ ngày" inline>
          <DatePicker value={from} onChange={setFrom} aria-label="Từ ngày" invalid={error !== null} />
        </Field>
        <Field label="Đến ngày" inline>
          <DatePicker value={to} onChange={setTo} aria-label="Đến ngày" invalid={error !== null} />
        </Field>
      </FilterBar>
      {error ? (
        <Callout tone="warn" role="alert">
          {error}
        </Callout>
      ) : (
        <PagedRows list={usage} what="mức sử dụng">
          <DataTable caption={USAGE_TABLE} columns={columns} rows={usage.items} rowKey={(u) => `${u.tenant_id}:${u.usage_date}`} empty="Không có số liệu trong khoảng ngày này." />
        </PagedRows>
      )}
    </>
  );
}
