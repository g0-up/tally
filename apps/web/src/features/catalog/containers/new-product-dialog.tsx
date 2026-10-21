import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { productFormErrors } from "../api/mutations";
import { skuKeys, useUnits } from "../api/queries";
import { firstProductError, initialProductDraft, productRequest, useProductAttributes, validateProductDraft, type ProductDraft, type ProductErrors } from "../components/product-draft";
import { ProductFormFields } from "../components/product-form";
import { UnitPricePreview } from "../components/unit-price-table";
import { productDetailSchema } from "../schemas";

const TITLE = "Thêm hàng hóa";

/** Prototype newSKU: one goods product with its SKU, units, prices and attributes. Mount per opening. */
export function NewProductDialog({ onClose }: { onClose: () => void }) {
  const units = useUnits();
  if (units.isLoading) {
    return (
      <CommitDialog open onClose={onClose} title={TITLE} wide>
        <ListLoading />
      </CommitDialog>
    );
  }
  return <NewProductForm onClose={onClose} initial={initialProductDraft("catalog", units.idByName)} />;
}

function NewProductForm({ onClose, initial }: { onClose: () => void; initial: ProductDraft }) {
  const client = useApiClient();
  const [draft, setDraft] = useState(initial);
  const [errors, setErrors] = useState<ProductErrors>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  const attributes = useProductAttributes(draft.categoryId);

  const commit = useCommit({
    mutationFn: async (d: ProductDraft, { idempotencyKey }) => {
      try {
        return await client.post("/products", productRequest(d, attributes), { idempotencyKey, schema: productDetailSchema });
      } catch (e) {
        setErrors(productFormErrors(e));
        throw e;
      }
    },
    success: () => "Đã thêm hàng hóa.",
    invalidate: [skuKeys.all],
    after: onClose,
  });

  const submit = () => {
    const found = validateProductDraft(
      draft,
      "catalog",
      attributes.filter((a) => a.required).map((a) => a.attribute.code),
    );
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
    <CommitDialog open onClose={onClose} title={TITLE} submitLabel="Lưu" onSubmit={submit} pending={commit.pending} wide live={<UnitPricePreview value={draft} />}>
      <ProductFormFields variant="catalog" draft={draft} onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))} errors={errors} attributes={attributes} />
    </CommitDialog>
  );
}
