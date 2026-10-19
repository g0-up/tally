import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import { useColorDirectory, useSkusByIds } from "../../catalog";
import { SupplierQuickDialog } from "../../partners";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { usePostReceipt } from "../api/mutations";
import { useReceivableLines } from "../api/queries";
import { buyTotal, emptyBuyLine, newProductCount, orderBuyLine, overTolerance, receiptLinesRequest, sellChanges, type BuyLine } from "../buy-lines";
import { BuyEditor } from "../components/buy-preset";
import { SupplierField, type PickedSupplier } from "../components/supplier-field";
import type { Order } from "../schemas";

export interface NewReceiptDialogProps {
  /** Receiving against this order; null for "Nhập nhanh". */
  order: Order | null;
  onClose: () => void;
}

const titleOf = (order: Order | null) => (order ? `Nhận hàng theo ${order.doc_no}` : "Nhập nhanh");

/** Goods were entered or changed since the form opened: an empty "Nhập nhanh" row is not an entry. */
const hasEnteredLines = (lines: readonly BuyLine[], initial: readonly BuyLine[]) =>
  lines !== initial && lines.some((l) => l.isNew || l.sku !== null || l.order !== null);

/**
 * Prototype newRCV: receiving against an order (its pending lines in base units, at the ordered
 * price) or "Nhập nhanh" without one. Mount per opening; the rows of an order start once its
 * pending lines and their SKUs are loaded. Closing after goods were entered asks first.
 */
export function NewReceiptDialog({ order, onClose }: NewReceiptDialogProps) {
  const pending = useReceivableLines(order?.id ?? null);
  const skus = useSkusByIds(pending.data?.map((l) => l.sku_id) ?? []);
  const colors = useColorDirectory();

  if (order === null) return <ReceiptForm order={null} initial={[emptyBuyLine()]} onClose={onClose} />;
  const loadingSkus = (pending.data?.length ?? 0) > 0 && skus.isPending && !skus.isError;
  if (pending.isPending || loadingSkus) {
    return (
      <CommitDialog open onClose={onClose} wide title={titleOf(order)}>
        <ListLoading />
      </CommitDialog>
    );
  }
  const initial = (pending.data ?? []).map((ol) => {
    const color = colors.codeOf(ol.color_id);
    return orderBuyLine(ol, skus.data?.get(ol.sku_id) ?? null, color === "none" ? null : color);
  });
  return <ReceiptForm order={order} initial={initial} onClose={onClose} />;
}

interface ReceiptFormProps {
  order: Order | null;
  initial: BuyLine[];
  onClose: () => void;
}

function ReceiptForm({ order, initial, onClose }: ReceiptFormProps) {
  const colors = useColorDirectory();
  const [supplier, setSupplier] = useState<PickedSupplier | null>(null);
  const [invoiceNo, setInvoiceNo] = useState("");
  const [freight, setFreight] = useState<Money | null>(null);
  const [lines, setLines] = useState<BuyLine[]>(initial);
  const [quick, setQuick] = useState(false);
  const post = usePostReceipt(onClose);
  const closeWarning = post.unsettled ? CLOSE_UNSETTLED_MESSAGE : hasEnteredLines(lines, initial) ? PROCUREMENT_MESSAGES.discardReceipt : null;

  const submit = () => {
    if (order === null && supplier === null) {
      toast.commitError(PROCUREMENT_MESSAGES.supplierRequired);
      return;
    }
    if (freight !== null && !dec(freight).isInteger()) {
      toast.commitError(PROCUREMENT_MESSAGES.freightWhole);
      return;
    }
    const built = receiptLinesRequest(lines, colors.idOf);
    if (!built.ok) {
      toast.commitError(built.message);
      return;
    }
    void post.run({ orderId: order?.id ?? null, supplierId: order?.supplier_party_id ?? supplier?.id ?? null, lines: built.lines, sells: sellChanges(lines), invoiceNo, freight });
  };

  return (
    <>
      <CommitDialog
        open
        onClose={onClose}
        {...(closeWarning !== null ? { closeWarning } : {})}
        wide
        title={titleOf(order)}
        sub={order ? `${order.supplier_name} · số lượng theo đơn vị cơ sở` : "Không cần đơn nhập. Chọn hàng có sẵn hoặc thêm hàng hóa mới."}
        submitLabel="Ghi sổ nhập kho"
        onSubmit={submit}
        pending={post.pending}
        live={
          <ImpactSummary
            items={[
              { label: "Nợ NCC tăng", value: vnd(buyTotal(lines)) },
              { label: "Chi phí mua vào giá vốn", value: vnd(freight) },
              { label: "Hàng hóa mới sẽ tạo", value: `${newProductCount(lines)} mặt hàng` },
              { label: "Vượt dung sai 5%", value: lines.some(overTolerance) ? "Có – sẽ bị chặn" : "Không" },
            ]}
          />
        }
      >
        <FormGrid>
          {order ? null : <SupplierField value={supplier} onChange={setSupplier} onQuick={() => setQuick(true)} />}
          <Field label="Số hóa đơn NCC (nếu có)">
            <Input value={invoiceNo} onChange={(e) => setInvoiceNo(e.target.value)} />
          </Field>
          <Field label="Chi phí vận chuyển/bốc xếp" hint="Phân bổ vào giá vốn theo giá trị">
            <MoneyInput value={freight} onChange={setFreight} />
          </Field>
        </FormGrid>
        <BuyEditor lines={lines} onChange={setLines} receive fixed={order !== null} />
      </CommitDialog>
      {quick ? (
        <SupplierQuickDialog
          onClose={() => setQuick(false)}
          onCreated={(s) => {
            setSupplier({ id: s.id, name: s.name });
            setQuick(false);
          }}
        />
      ) : null}
    </>
  );
}
