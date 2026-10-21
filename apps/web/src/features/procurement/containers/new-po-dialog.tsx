import { useState } from "react";
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
import { toast } from "../../../shared/ui/toast-store";
import { useColorDirectory } from "../../catalog";
import { SupplierQuickDialog } from "../../partners";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { useSaveOrder } from "../api/mutations";
import { barcodeErrors, buyTotal, emptyBuyLine, newProductCount, orderLinesRequest, sellChanges, type BuyLine, type SentWrite } from "../buy-lines";
import { BuyEditor } from "../components/buy-preset";
import { SupplierField, type PickedSupplier } from "../components/supplier-field";

export interface NewPoDialogProps {
  /** Supplier chosen beforehand (Nhà cung cấp → "Tạo đơn nhập"). */
  supplier?: PickedSupplier | null;
  onClose: () => void;
}

/** Days after the business date an order is expected by default (prototype newPO). */
const DEFAULT_LEAD_DAYS = 5;

/** Prototype newPO: supplier, expected date and the buy lines; saved as a draft or saved and confirmed. */
export function NewPoDialog({ supplier: preset = null, onClose }: NewPoDialogProps) {
  const today = useBusinessDate();
  const colors = useColorDirectory();
  const [supplier, setSupplier] = useState<PickedSupplier | null>(preset);
  const [expected, setExpected] = useState<string | null>(() => addDays(today, DEFAULT_LEAD_DAYS));
  const [lines, setLines] = useState<BuyLine[]>(() => [emptyBuyLine()]);
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
    void save.run({ supplierId: supplier.id, expectedDate: expected, lines: built.lines, sells: sellChanges(lines), confirm });
  };

  const fresh = newProductCount(lines);

  return (
    <>
      <CommitDialog
        open
        onClose={onClose}
        {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
        wide
        title="Tạo đơn nhập"
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
