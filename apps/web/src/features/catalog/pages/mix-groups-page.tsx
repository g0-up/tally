import { useMemo, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { dec, floorDec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { errorMessage } from "../../../shared/api/api-error";
import { mixGroupKeys, skuKeys, useMixGroups, useSkusByIds, useUnits } from "../api/queries";
import { MixGroupConfirmDialog, type MixGroupConfirmKind } from "../containers/mix-group-confirm-dialog";
import { MixGroupDialog } from "../containers/mix-group-dialog";
import type { MixGroup, SkuListItem } from "../schemas";

const STATUS_LABELS = { active: "Đang dùng", inactive: "Ngừng dùng" } as const;

type Opened = { kind: "edit"; group: MixGroup | null } | { kind: MixGroupConfirmKind; group: MixGroup } | null;

/** Prototype mixRange: lowest – highest retail price of the flavors times the multiplier. */
function priceRange(group: MixGroup, skus: ReadonlyMap<string, SkuListItem> | undefined, mult: number): string {
  const xs = group.items.flatMap((x) => {
    const p = skus?.get(x.sku_id)?.retail_price;
    return p ? [dec(p).times(mult)] : [];
  });
  if (xs.length === 0) return "—";
  const lo = xs.reduce((a, b) => (b.lt(a) ? b : a));
  const hi = xs.reduce((a, b) => (b.gt(a) ? b : a));
  return lo.eq(hi) ? vnd(lo) : `${vnd(lo)} – ${vnd(hi)}`;
}

/** Nhóm mix vị (/catalog/mix-groups). */
export default function MixGroupsPage() {
  const client = useApiClient();
  const groups = useMixGroups();
  const units = useUnits();
  const [opened, setOpened] = useState<Opened>(null);
  // The group whose "Dùng lại" is in flight: only its button shows the wait.
  const [reactivating, setReactivating] = useState<string | null>(null);
  const flavorIds = useMemo(() => (groups.data ?? []).flatMap((g) => g.items.map((x) => x.sku_id)), [groups.data]);
  const skus = useSkusByIds(flavorIds);

  const reactivate = useCommit({
    mutationFn: (g: MixGroup, { idempotencyKey }) => client.post<unknown>(`/mix-groups/${g.id}:reactivate`, undefined, { idempotencyKey }),
    success: () => "Đã dùng lại nhóm mix.",
    invalidate: [mixGroupKeys.all, skuKeys.all],
  });

  const columns: Column<MixGroup>[] = [
    { key: "name", header: "Nhóm mix", cell: (g) => <b style={{ color: "var(--navy-900)" }}>{g.name}</b> },
    {
      key: "spec",
      header: "Quy cách",
      cell: (g) => {
        const pack = units.name(g.pack_unit_id);
        const part = units.name(g.part_unit_id);
        const piece = units.name(g.piece_unit_id);
        return (
          <>
            1 {pack} = {num(g.units_per_pack / g.units_per_part)} {part} = {num(g.units_per_pack)} {piece}
            <CellSub>
              chọn vị theo {part} ({g.units_per_part} {piece})
            </CellSub>
          </>
        );
      },
    },
    {
      key: "flavors",
      header: "Các vị · tồn có thể bán",
      cell: (g) => {
        const part = units.name(g.part_unit_id);
        return g.items.map((x, i) => {
          const sellable = skus.data?.get(x.sku_id)?.sellable;
          return (
            <span key={x.sku_id}>
              {i > 0 ? " " : null}
              <Badge>
                {x.flavor_label} · {sellable === undefined ? "—" : num(floorDec(dec(sellable).div(g.units_per_part)))} {part}
              </Badge>
            </span>
          );
        });
      },
    },
    { key: "price", header: "Giá lẻ / thùng", numeric: true, cell: (g) => priceRange(g, skus.data, g.units_per_pack) },
    {
      key: "usage",
      header: "Đang dùng trên",
      cell: (g) => (g.usage.length > 0 ? g.usage.map((u) => `${u.count} ${u.label}`).join(", ") : <span className="hint">Chưa dùng</span>),
    },
    { key: "status", header: "Trạng thái", cell: (g) => <StatusPill label={STATUS_LABELS[g.status]} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (g) => (
        <ButtonRow style={{ flexWrap: "nowrap", whiteSpace: "nowrap" }}>
          <Button variant="navy" size="sm" aria-label={`Sửa ${g.name}`} onClick={() => setOpened({ kind: "edit", group: g })}>
            Sửa
          </Button>
          {g.status === "active" ? (
            <Button size="sm" aria-label={`Ngừng dùng ${g.name}`} onClick={() => setOpened({ kind: "deactivate", group: g })}>
              Ngừng dùng
            </Button>
          ) : (
            <Button
              size="sm"
              aria-label={`Dùng lại ${g.name}`}
              onClick={() => {
                setReactivating(g.id);
                void reactivate.run(g);
              }}
              // One write at a time: the other rows wait, only this one says so.
              disabled={reactivate.pending}
              pending={reactivate.pending && reactivating === g.id}
            >
              Dùng lại
            </Button>
          )}
          {g.usage.length > 0 ? null : (
            <Button variant="danger" size="sm" aria-label={`Xóa ${g.name}`} onClick={() => setOpened({ kind: "delete", group: g })}>
              Xóa
            </Button>
          )}
        </ButtonRow>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        lead="Một nhóm mix gom các vị cùng quy cách để bán theo thùng mix: khách chọn số dây/lốc từng vị, tổng không vượt số thùng. Nhóm xuất hiện trong ô chọn ở mọi dòng “Thùng mix vị” của báo giá, hóa đơn, giỏ quà và combo."
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "edit", group: null })}>
            Thêm nhóm mix
          </Button>
        }
      />
      {groups.isPending ? (
        <ListLoading />
      ) : groups.error ? (
        <Callout tone="bad" role="alert">
          Không tải được danh sách: {errorMessage(groups.error)}
        </Callout>
      ) : (
        <DataTable caption="Nhóm mix vị" columns={columns} rows={groups.data} rowKey={(g) => g.id} empty="Chưa có nhóm mix vị nào. Bấm “Thêm nhóm mix”." />
      )}
      {opened?.kind === "edit" ? <MixGroupDialog group={opened.group} groups={groups.data ?? []} onClose={() => setOpened(null)} /> : null}
      {opened !== null && opened.kind !== "edit" ? <MixGroupConfirmDialog group={opened.group} kind={opened.kind} onClose={() => setOpened(null)} /> : null}
    </>
  );
}
