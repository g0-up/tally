import { useQueries } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";
import { fd, money } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useSkuList, type SkuListItem } from "../../catalog";
import { priceListKeys, useCustomerGroups, usePriceLists } from "../api/queries";
import { LoadError } from "../components/load-error";
import { byPrecedence, scopeLabel, tierLabel } from "../price-list-scope";
import { priceListItemSchema, type PriceList } from "../schemas";

const SECTION_STYLE = { margin: "22px 0 10px" } as const;
/** Rows read per non-base list to find its quantity tiers. */
const TIER_ITEMS_LIMIT = 200;

interface ListRow {
  rank: number;
  list: PriceList;
  scope: string;
  tier: string;
}

const listColumns: Column<ListRow>[] = [
  { key: "rank", header: "Ưu tiên", numeric: true, cell: (r) => r.rank },
  {
    key: "name",
    header: "Bảng giá",
    cell: (r) => (
      <>
        {r.list.name}
        {r.tier ? <CellSub>Bậc thang: {r.tier}</CellSub> : null}
      </>
    ),
  },
  { key: "scope", header: "Áp dụng", cell: (r) => r.scope },
  { key: "valid", header: "Hiệu lực", cell: (r) => `${fd(r.list.valid_from)} – ${r.list.valid_to === null ? "không thời hạn" : fd(r.list.valid_to)}` },
];

const priceOrDash = (v: string | null) => (v === null ? "—" : money(v));

const skuColumns: Column<SkuListItem>[] = [
  { key: "sku", header: "Hàng", cell: (s) => s.display_name },
  { key: "retail", header: "Giá lẻ", numeric: true, cell: (s) => priceOrDash(s.retail_price) },
  { key: "wholesale", header: "Giá sỉ / doanh nghiệp", numeric: true, cell: (s) => priceOrDash(s.wholesale_price) },
  { key: "min", header: "Giá tối thiểu (cảnh báo nếu bán thấp hơn)", numeric: true, cell: (s) => priceOrDash(s.min_price) },
  { key: "cost", header: "Giá vốn BQ", numeric: true, cell: (s) => priceOrDash(s.avg_cost) },
];

/** Bảng giá (/catalog/price-lists): read-only view of the lists and the base prices per SKU. */
export default function PriceListsPage() {
  const client = useApiClient();
  const lists = usePriceLists();
  const groups = useCustomerGroups();
  const ordered = byPrecedence(lists.data ?? []);
  // Base lists hold one row per SKU; tiers live on group, customer and season lists.
  const tiered = ordered.filter((l) => l.kind !== "retail" && l.kind !== "wholesale" && l.item_count > 0);
  const tiers = useQueries({
    queries: tiered.map((l) => ({
      queryKey: priceListKeys.items(l.id, null),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        client.get(`/price-lists/${l.id}/items`, { query: { limit: TIER_ITEMS_LIMIT }, schema: cursorPageSchema(priceListItemSchema), signal }),
    })),
  });
  const failedTiers = tiers.filter((t) => t.error);
  const tierOf = new Map(tiered.map((l, i) => [l.id, tierLabel(tiers[i]?.data?.data ?? [])]));
  const rows: ListRow[] = ordered.map((list, i) => ({ rank: i + 1, list, scope: scopeLabel(list, groups.data ?? []), tier: tierOf.get(list.id) ?? "" }));

  const skus = useSkuList({});
  const priced = skus.items.filter((s) => s.retail_price !== null);

  return (
    <>
      <PageHeader lead="Giá được xác định theo thứ tự ưu tiên: giá riêng khách → bảng giá nhóm khách → giá lẻ. Mỗi dòng đơn hiển thị nguồn giá và chụp giá khi xác nhận." />
      {lists.error ? (
        <LoadError what="bảng giá" error={lists.error} onRetry={() => void lists.refetch()} />
      ) : lists.isPending ? (
        <ListLoading />
      ) : (
        <DataTable caption="Bảng giá" columns={listColumns} rows={rows} rowKey={(r) => r.list.id} />
      )}
      {/* Scopes and tiers fall back to blanks without these reads, so say they are missing. */}
      {groups.error ? <LoadError what="nhóm khách" error={groups.error} onRetry={() => void groups.refetch()} /> : null}
      {failedTiers[0] ? (
        <LoadError what="bậc thang giá" error={failedTiers[0].error} onRetry={() => failedTiers.forEach((t) => void t.refetch())} />
      ) : null}
      <h3 className="sec-h" style={SECTION_STYLE}>
        Giá theo hàng
      </h3>
      {/* A failed first page shows only the footer's error, not an empty table. */}
      {skus.isLoading ? <ListLoading /> : skus.error && priced.length === 0 ? null : <DataTable caption="Giá theo hàng" columns={skuColumns} rows={priced} rowKey={(s) => s.id} />}
      <ListFooter list={skus} />
    </>
  );
}
