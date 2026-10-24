import { useMemo, useState } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { addDays } from "../../../shared/lib/dates";
import { vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { useColorDirectory, useSkusByIds } from "../../catalog";
import { SupplierQuickDialog } from "../../partners";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { useSaveOrder } from "../api/mutations";
import { useOrder } from "../api/queries";
import { barcodeErrors, buyTotal, draftOrderBuyLine, emptyBuyLine, newProductCount, orderLinesRequest, sellChanges, type BuyLine, type SentWrite } from "../buy-lines";
import { BuyEditor } from "../components/buy-preset";
import { SupplierField, type PickedSupplier } from "../components/supplier-field";
import type { Order } from "../schemas";

export interface NewPoDialogProps {
  /** Supplier chosen beforehand (Nhà cung cấp → "Tạo đơn nhập"). */
  supplier?: PickedSupplier | null;
  /** The draft to edit ("Sửa"), as listed; its lines are read when the dialog opens. */
  order?: Order | null;
  onClose: () => void;
}

/** Days after the business date an order is expected by default (prototype newPO). */
const DEFAULT_LEAD_DAYS = 5;

/**
 * Prototype newPO: supplier, expected date and the buy lines; saved as a draft or saved and confirmed.
 * "Sửa" on a draft opens it here prefilled once its lines, their SKUs and colors are loaded.
 */
export function NewPoDialog({ supplier = null, order = null, onClose }: NewPoDialogProps) {
  const today = useBusinessDate();
  if (order !== null) return <EditPoDialog order={order} onClose={onClose} />;
  return <PoForm order={null} supplier={supplier} expected={addDays(today, DEFAULT_LEAD_DAYS)} initial={[emptyBuyLine()]} onClose={onClose} />;
}

/** The draft as read when the edit dialog opened, with its lines as buy lines. */
interface OpenedDraft {
  order: Order;
  initial: BuyLine[];
}

const SKUS_MISSING = "Không đọc được đủ hàng hóa của đơn. Tải lại trang rồi thử lại.";

function EditPoDialog({ order, onClose }: { order: Order; onClose: () => void }) {
  const detail = useOrder(order.id);
  const lines = useMemo(() => detail.data?.lines ?? [], [detail.data]);
  const skus = useSkusByIds(useMemo(() => lines.map((l) => l.sku_id), [lines]));
  const colors = useColorDirectory();
  // The form keeps the draft it opened on: the refreshes its own saves cause must neither reset what
  // was typed nor drop the progress a retry continues from. A save after a change elsewhere is refused
  // (412) and its retry re-reads the order.
  const [opened, setOpened] = useState<OpenedDraft | null>(null);

  if (opened !== null) {
    const o = opened.order;
    return <PoForm order={o} supplier={{ id: o.supplier_party_id, name: o.supplier_name }} expected={o.expected_date ?? null} initial={opened.initial} onClose={onClose} />;
  }

  // A cached copy may be older than the stored draft, so the form waits for this read.
  const d = detail.isFetchedAfterMount ? detail.data : undefined;
  // Until the color list is in, every line reads as uncolored and a save would strip the colors.
  const needsColors = lines.some((l) => l.color_id);
  const error = detail.error ?? skus.error ?? (needsColors ? colors.error : null);
  const loading = !d || (lines.length > 0 && skus.isPending) || (needsColors && colors.pending);
  const refusal = error
    ? errorMessage(error)
    : loading
      ? null
      : d.status !== "draft"
        ? PROCUREMENT_MESSAGES.orderNotDraft
        : // A line without its SKU would be dropped, and the save would remove it from the order.
          lines.some((l) => !skus.data?.has(l.sku_id))
          ? SKUS_MISSING
          : null;
  if (loading || refusal !== null) {
    return (
      <CommitDialog open onClose={onClose} wide title={`Sửa đơn nhập ${order.doc_no}`}>
        {refusal !== null ? <p className="hint">{refusal}</p> : <ListLoading />}
      </CommitDialog>
    );
  }
  const initial = lines.map((ol) => {
    const color = colors.codeOf(ol.color_id);
    return draftOrderBuyLine(ol, skus.data?.get(ol.sku_id) ?? null, color === "none" ? null : color);
  });
  setOpened({ order: d, initial: initial.length > 0 ? initial : [emptyBuyLine()] });
  return null;
}

interface PoFormProps {
  /** The draft being edited, with its lines; null for a new order. */
  order: Order | null;
  supplier: PickedSupplier | null;
  expected: string | null;
  initial: BuyLine[];
  onClose: () => void;
}

function PoForm({ order, supplier: initialSupplier, expected: initialExpected, initial, onClose }: PoFormProps) {
  const colors = useColorDirectory();
  const [supplier, setSupplier] = useState<PickedSupplier | null>(initialSupplier);
  const [expected, setExpected] = useState<string | null>(initialExpected);
  const [lines, setLines] = useState<BuyLine[]>(initial);
  const focusFirstInvalid = useFocusFirstInvalid();
  const [quick, setQuick] = useState(false);
  const save = useSaveOrder(onClose);
  /** Lines of the last save, so a refused barcode shows under the row that sent it. */
  const [sent, setSent] = useState<SentWrite | null>(null);

  const submit = (confirm: boolean) => {
    if (supplier === null) {
      toast.commitError(PROCUREMENT_MESSAGES.supplierRequired);
      return;
    }
    const built = orderLinesRequest(lines, colors.idOf);
    if (!built.ok) {
      toast.commitError(built.message);
      // A refused unit table opens under its row with the errors marked.
      if (built.rows) {
        setLines(built.rows);
        focusFirstInvalid();
      }
      return;
    }
    setSent({ lines: built, before: save.error });
    void save.run({ order, supplierId: supplier.id, expectedDate: expected, lines: built.lines, sells: sellChanges(lines), confirm });
  };

  const fresh = newProductCount(lines);

  return (
    <>
      <CommitDialog
        open
        onClose={onClose}
        {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
        wide
        title={order ? `Sửa đơn nhập ${order.doc_no}` : "Tạo đơn nhập"}
        extra={
          <Button variant="navy" onClick={() => submit(false)} disabled={save.pending}>
            Lưu nháp
          </Button>
        }
        submitLabel="Lưu & chốt đơn"
        onSubmit={() => submit(true)}
        pending={save.pending}
        live={
          <ImpactSummary
            items={[
              { label: "Giá trị đơn nhập", value: vnd(buyTotal(lines)) },
              { label: "Hàng hóa mới sẽ tạo", value: fresh ? `${fresh} mặt hàng` : "Không" },
              { label: "Tồn / nợ NCC", value: "Chưa phát sinh" },
            ]}
          />
        }
      >
        <FormGrid>
          <SupplierField value={supplier} onChange={setSupplier} onQuick={() => setQuick(true)} />
          <Field label="Ngày dự kiến giao">
            <DatePicker value={expected} onChange={setExpected} />
          </Field>
        </FormGrid>
        <BuyEditor lines={lines} onChange={setLines} barcodeErrors={barcodeErrors(save.error, sent)} busy={save.pending} />
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
