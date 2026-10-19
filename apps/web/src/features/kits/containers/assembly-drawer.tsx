import { useMemo } from "react";
import { fd, num, vnd } from "../../../shared/lib/format";
import { ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Stepper } from "../../../shared/ui/stepper";
import { useColorDirectory } from "../../catalog";
import { ASSEMBLY_STATUS_LABELS, ASSEMBLY_STEPS, assemblyStepsDone } from "../api/adapters";
import { useAssembly, useGenealogies } from "../api/queries";
import type { AssemblyDetail, PostingItem } from "../schemas";

export interface AssemblyDrawerProps {
  orderId: string;
  /** "SP-…" of the invoice the order was planned for, when known. */
  invoiceDoc: (invoiceId: string) => string | undefined;
  onClose: () => void;
}

/**
 * Prototype `openASM`: an assembly order's steps and counts, and each posting with the kit lot it
 * produced and the component lots it consumed.
 */
export function AssemblyDrawer({ orderId, invoiceDoc, onClose }: AssemblyDrawerProps) {
  const query = useAssembly(orderId);
  const order = query.data;
  const lotIds = useMemo(() => order?.postings.map((p) => p.output_lot_id) ?? [], [order]);
  const genealogies = useGenealogies(lotIds);
  const colors = useColorDirectory();

  if (!order) {
    return (
      <DetailDrawer open onClose={onClose} title="Lệnh đóng">
        {query.isError ? <DrawerError message="Không tải được lệnh đóng." /> : <p className="hint">Đang tải…</p>}
      </DetailDrawer>
    );
  }

  const plan = num(order.planned_qty);
  const done = `${num(order.completed_qty)}/${plan}`;
  const so = order.invoice_id ? invoiceDoc(order.invoice_id) : undefined;
  const nameOf = (skuId: string) => order.items.find((i) => i.sku_id === skuId)?.sku_name ?? "";
  const expiryOf = (lotId: string) => [...genealogies.values()].flatMap((g) => g.components).find((c) => c.child_lot_id === lotId)?.expiry_date ?? null;

  const columns: Column<PostingItem>[] = [
    {
      key: "sku",
      header: "Thành phần",
      cell: (u) => (
        <>
          {nameOf(u.sku_id)}
          {u.color_id ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(u.color_id)} />
            </>
          ) : null}
        </>
      ),
    },
    { key: "lot", header: "Lô", cell: (u) => u.lot_no },
    { key: "exp", header: "Hạn", cell: (u) => fd(expiryOf(u.lot_id)) },
    { key: "qty", header: "SL", numeric: true, cell: (u) => num(u.consumed_qty) },
  ];

  return (
    <DetailDrawer open onClose={onClose} title={`Lệnh đóng ${order.doc_no}`} sub={`${order.kit_name} · kế hoạch ${plan}`} docKey={order.doc_no}>
      <ButtonRow>
        <StatusPill label={ASSEMBLY_STATUS_LABELS[order.status]} />
      </ButtonRow>
      <Stepper steps={ASSEMBLY_STEPS} done={assemblyStepsDone(order.status)} showCurrent={false} label="Các bước lệnh đóng" />
      <ImpactSummary
        items={[
          { label: "Soạn nguyên liệu", value: done },
          { label: "Đã đóng", value: done },
          { label: "QC đạt", value: done },
          { label: "Đã nhập kho", value: done },
        ]}
      />
      <p className="hint">Đóng xong không có nghĩa là đã giao. Trạng thái giao theo phiếu giao của đơn {so ?? ""}.</p>
      <h3 className="sec-h">Các lần ghi sổ & truy xuất lô</h3>
      {order.postings.length === 0 ? <p className="hint">Chưa có lần hoàn thành.</p> : order.postings.map((p) => <PostingCard key={p.id} order={order} posting={p} columns={columns} />)}
    </DetailDrawer>
  );
}

function PostingCard({ order, posting: p, columns }: { order: AssemblyDetail; posting: AssemblyDetail["postings"][number]; columns: Column<PostingItem>[] }) {
  return (
    <Card
      style={{ boxShadow: "none", border: "1px solid var(--gray-200)" }}
      title={`${fd(p.business_date)} · ${num(p.produced_qty)} giỏ · lô giỏ ${p.output_lot_no}`}
      note={`Hạn bán ${fd(p.output_expiry)} · giá vốn ${vnd(p.output_unit_cost)}/giỏ · công thức v${order.recipe_version}`}
    >
      <DataTable caption={`Lô thành phần của lô giỏ ${p.output_lot_no}`} columns={columns} rows={p.items} rowKey={(u) => u.id} flat />
    </Card>
  );
}
