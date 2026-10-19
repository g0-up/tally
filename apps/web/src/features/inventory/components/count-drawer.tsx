import { useMemo, useState, type KeyboardEvent } from "react";
import { colorLabel, isColorCode } from "../../../shared/domain/color";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { fdt } from "../../../shared/lib/dates";
import { num, vnd } from "../../../shared/lib/format";
import { LockNote } from "../../../shared/ui/badge";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { QtyInput } from "../../../shared/ui/qty-input";
import { StatusPill } from "../../../shared/ui/status-pill";
import { toast } from "../../../shared/ui/toast-store";
import { useSkusByIds } from "../../catalog";
import { colorKeyOf, COUNT_STATUS_LABELS } from "../api/adapters";
import { signedQty, useCountLineSaver, useFinishCount, usePostCount, useRecount, type CountLineEntry } from "../api/mutations";
import { useCount } from "../api/queries";
import type { Count, CountLine } from "../schemas";
import { countScopeLabel } from "../view-model";

export interface CountDrawerProps {
  id: string;
  categoryName: (id: string | null | undefined) => string | null;
  onClose: () => void;
}

const qtyEq = (a: Qty | null | undefined, b: Qty | null | undefined) => (a === null || a === undefined || b === null || b === undefined ? (a ?? null) === (b ?? null) : dec(a).eq(b));

/** What tells one count line from another: product, variant, color and lot ("Kẹo dừa · Đỏ · lô L01"). */
function countLineName(l: CountLine): string {
  const color = l.color_name ?? (isColorCode(l.color_code) ? colorLabel(l.color_code) : null);
  return [l.sku_name, l.variant_label, color, l.lot_no ? `lô ${l.lot_no}` : null].filter(Boolean).join(" · ");
}

/**
 * Prototype `drawCount`: the lines of one count. While counting each typed quantity is saved when
 * the field is left; the scanner adds one to the line of the scanned code and says which line, for
 * a cashier who is looking at the goods rather than the screen. "Số hệ thống" reads "Ẩn khi đếm
 * mù" whenever the server leaves it out.
 */
export function CountDrawer({ id, categoryName, onClose }: CountDrawerProps) {
  const query = useCount(id);
  const count = query.data?.id === id ? query.data : undefined;
  const lines = useMemo(() => count?.lines ?? [], [count]);
  const skus = useSkusByIds(useMemo(() => lines.map((l) => l.sku_id), [lines]));
  const [edits, setEdits] = useState<Readonly<Record<string, Qty | null>>>({});
  const [lots, setLots] = useState<Readonly<Record<string, string>>>({});
  const [scan, setScan] = useState("");
  const [scanned, setScanned] = useState("");
  const saver = useCountLineSaver(count);
  const finish = useFinishCount(saver.flush);
  const post = usePostCount();
  const recount = useRecount();
  const editing = count?.status === "counting";

  const valueOf = (l: CountLine) => (l.id in edits ? (edits[l.id] ?? null) : (l.counted_qty ?? null));
  const entry = (l: CountLine, counted: Qty | null): CountLineEntry => {
    const lot = lots[l.id]?.trim();
    return { line_id: l.id, counted_qty: counted, ...(lot ? { lot_no: lot } : {}) };
  };
  const pending = (): CountLineEntry[] => lines.filter((l) => l.id in edits && !qtyEq(edits[l.id], l.counted_qty)).map((l) => entry(l, edits[l.id] ?? null));
  const commitLine = (l: CountLine) => {
    if (!(l.id in edits) || qtyEq(edits[l.id], l.counted_qty)) return;
    void saver.save([entry(l, edits[l.id] ?? null)]);
  };

  const onScan = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    const code = scan.trim().toLowerCase();
    if (code === "") return;
    const line = lines.find((l) => {
      const sku = skus.data?.get(l.sku_id);
      return l.sku_code.toLowerCase() === code || sku?.barcode?.toLowerCase() === code;
    });
    if (!line) {
      setScanned("");
      toast.error(`Không có mã ${scan.trim()} trong phiên kiểm kê.`);
      return;
    }
    const next = toQty(dec(valueOf(line) ?? 0).plus(1));
    setEdits((cur) => ({ ...cur, [line.id]: next }));
    setScanned(`Đã cộng 1 – ${countLineName(line)}, thực đếm ${num(next)}`);
    setScan("");
    void saver.save([entry(line, next)]);
  };

  const columns: Column<CountLine>[] = [
    {
      key: "sku",
      header: "Hàng",
      cell: (l) => (
        <>
          {l.sku_name}
          {l.variant_label ? ` · ${l.variant_label}` : ""}
          {l.color_id ? (
            <>
              {" "}
              <ColorChip color={colorKeyOf(l.color_code)} />
            </>
          ) : null}
          {l.lot_no ? <CellSub>Lô {l.lot_no}</CellSub> : null}
          {editing && l.track_lot && !l.lot_id ? (
            <Input
              size="md"
              value={lots[l.id] ?? ""}
              onChange={(e) => setLots((cur) => ({ ...cur, [l.id]: e.target.value }))}
              placeholder="Số lô"
              aria-label={`Số lô – ${countLineName(l)}`}
            />
          ) : null}
        </>
      ),
    },
    {
      key: "system",
      header: "Số hệ thống",
      numeric: true,
      cell: (l) => (l.system_qty === null || l.system_qty === undefined ? <LockNote>Ẩn khi đếm mù</LockNote> : num(l.system_qty)),
    },
    {
      key: "counted",
      header: "Thực đếm",
      numeric: true,
      cell: (l) =>
        editing ? (
          <QtyInput size="sm" value={valueOf(l)} onChange={(v) => setEdits((cur) => ({ ...cur, [l.id]: v }))} onBlur={() => commitLine(l)} aria-label={`Thực đếm – ${countLineName(l)}`} />
        ) : l.counted_qty === null || l.counted_qty === undefined ? (
          "—"
        ) : (
          num(l.counted_qty)
        ),
    },
    {
      key: "diff",
      header: "Chênh lệch",
      numeric: true,
      cell: (l) =>
        l.variance_qty === null || l.variance_qty === undefined ? "—" : dec(l.variance_qty).isZero() ? "0" : <b className="dl bad">{signedQty(l.variance_qty)}</b>,
    },
    {
      key: "value",
      header: "Giá trị lệch",
      numeric: true,
      cell: (l) => {
        const cost = skus.data?.get(l.sku_id)?.avg_cost;
        if (!l.variance_qty || dec(l.variance_qty).isZero() || cost === null || cost === undefined) return "";
        return vnd(dec(l.variance_qty).times(cost));
      },
    },
  ];

  const actions = (c: Count) => (
    <ButtonRow>
      <StatusPill label={COUNT_STATUS_LABELS[c.status]} />
      {c.status === "counting" ? (
        <Button variant="primary" size="sm" pending={finish.pending} onClick={() => void finish.run({ pending: pending() }).then((r) => r && setEdits({}))}>
          Hoàn tất đếm
        </Button>
      ) : null}
      {c.status === "pending_post" ? (
        <>
          <Button variant="primary" size="sm" pending={post.pending} onClick={() => void post.run(c)}>
            Xác nhận & ghi sổ chênh lệch
          </Button>
          <Button size="sm" pending={recount.pending} onClick={() => void recount.run(c)}>
            Đếm lại
          </Button>
        </>
      ) : null}
    </ButtonRow>
  );

  return (
    <DetailDrawer
      open
      onClose={onClose}
      docKey={id}
      title={count ? `Kiểm kê ${count.doc_no}` : "Kiểm kê"}
      sub={count ? `${countScopeLabel(count, categoryName)} · cutoff ${fdt(count.started_at)}` : undefined}
    >
      {query.isError ? (
        <DrawerError message="Không tải được phiên kiểm kê." />
      ) : !count ? (
        <ListLoading />
      ) : (
        <>
          {actions(count)}
          {count.status === "pending_post" ? <p className="hint">Kiểm tra cột chênh lệch trước khi ghi sổ. Sau khi ghi, tồn kho được điều chỉnh theo số thực đếm.</p> : null}
          {editing ? (
            <div className="scan">
              <input value={scan} onChange={(e) => setScan(e.target.value)} onKeyDown={onScan} placeholder="Quét mã vạch hoặc gõ mã hàng rồi Enter" aria-label="Quét mã vạch" />
              <p className="sr-only" role="status">
                {scanned}
              </p>
            </div>
          ) : null}
          <DataTable caption={`Dòng kiểm kê ${count.doc_no}`} columns={columns} rows={lines} rowKey={(l) => l.id} flat empty="Phiên không có mặt hàng nào." />
        </>
      )}
    </DetailDrawer>
  );
}
