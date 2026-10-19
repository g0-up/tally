import { Button } from "../../../shared/ui/button";
import { Combobox } from "../../../shared/ui/combobox";
import { Field } from "../../../shared/ui/field";
import { activeSupplierSource } from "../../partners";

export interface PickedSupplier {
  id: string;
  name: string;
}

export interface SupplierFieldProps {
  value: PickedSupplier | null;
  onChange: (supplier: PickedSupplier) => void;
  /** "+ NCC mới": opens the quick supplier dialog over the document. */
  onQuick: () => void;
}

/** "Nhà cung cấp" of newPO and Nhập nhanh: trading suppliers only, plus the quick-add link. */
export function SupplierField({ value, onChange, onQuick }: SupplierFieldProps) {
  return (
    <Field
      label="Nhà cung cấp"
      hint={
        <>
          <Button variant="line" size="sm" onClick={onQuick}>
            + NCC mới
          </Button>{" "}
          hoặc quản lý ở Danh mục → Nhà cung cấp
        </>
      }
    >
      <Combobox
        kind="party"
        source={activeSupplierSource}
        value={value?.id ?? null}
        valueLabel={value?.name ?? ""}
        onChange={(it) => onChange({ id: it.id, name: it.name })}
        aria-label="Nhà cung cấp"
      />
    </Field>
  );
}
