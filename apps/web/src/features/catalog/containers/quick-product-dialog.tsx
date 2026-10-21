import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { productFormErrors } from "../api/mutations";
import { skuKeys, useUnits } from "../api/queries";
import { firstProductError, initialProductDraft, quickCreateRequest, validateProductDraft, type ProductDraft, type ProductErrors } from "../components/product-draft";
import { ProductFormFields } from "../components/product-form";
import { UnitPricePreview } from "../components/unit-price-table";
import { quickCreateResultSchema, type QuickCreateResult } from "../schemas";

const TITLE = "Thêm hàng hóa mới";

interface QuickProductDialogProps {
  onClose: () => void;
  /** Receives the created product, e.g. to put its SKU on the line being entered. */
  onCreated?: ((result: QuickCreateResult) => void) | undefined;
}

/** Prototype newQuickProd (Nhập hàng → Danh sách hàng hóa): name, units, purchase and sale prices. Mount per opening. */
export function QuickProductDialog({ onClose, onCreated }: QuickProductDialogProps) {
  const units = useUnits();
  if (units.isLoading) {
    return (
      <CommitDialog open onClose={onClose} title={TITLE} wide>
        <ListLoading />
      </CommitDialog>
    );
  }
  return <QuickProductForm onClose={onClose} onCreated={onCreated} initial={initialProductDraft("quick", units.idByName)} />;
}

function QuickProductForm({ onClose, onCreated, initial }: QuickProductDialogProps & { initial: ProductDraft }) {
  const client = useApiClient();
  const [draft, setDraft] = useState(initial);
  const [errors, setErrors] = useState<ProductErrors>({});
  const focusFirstInvalid = useFocusFirstInvalid();

  const commit = useCommit({
    mutationFn: async (d: ProductDraft, { idempotencyKey }) => {
      try {
        return await client.post("/products:quick-create", quickCreateRequest(d), { idempotencyKey, schema: quickCreateResultSchema });
      } catch (e) {
        setErrors(productFormErrors(e));
        throw e;
      }
    },
    success: (r) => (
      <>
        Đã thêm <b>{r.product.skus[0]?.code ?? r.product.name}</b>. Mã vạch được sinh tự động, có thể sửa ở Danh mục.
      </>
    ),
    invalidate: [skuKeys.all],
    after: (r) => {
      onCreated?.(r);
      onClose();
    },
  });

  const submit = () => {
    const found = validateProductDraft(draft, "quick");
    setErrors(found);
    const first = firstProductError(found);
    if (first !== undefined) {
      toast.commitError(first);
      focusFirstInvalid();
      return;
    }
    void commit.run(draft);
  };

  return (
    <CommitDialog open onClose={onClose} title={TITLE} submitLabel="Thêm" onSubmit={submit} pending={commit.pending} wide live={<UnitPricePreview value={draft} />}>
      <ProductFormFields variant="quick" draft={draft} onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))} errors={errors} attributes={[]} />
    </CommitDialog>
  );
}
