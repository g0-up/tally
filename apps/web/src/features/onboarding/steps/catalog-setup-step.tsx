import { useState } from "react";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { TextLink } from "../../../shared/ui/link-button";
import { QuickProductDialog } from "../../catalog";
import { StepCard } from "../components/step-card";
import type { ChecklistStep } from "../steps";

const CATALOG_LABELS = {
  addProduct: "Thêm hàng hóa",
  products: "Mở danh mục hàng hóa",
  suppliers: "Mở danh sách nhà cung cấp",
} as const;

/**
 * Catalog set-up by hand: products one by one through the quick product form, or the full catalog
 * screens. No file import.
 */
export function CatalogSetupStep({ step }: { step: ChecklistStep }) {
  const [adding, setAdding] = useState(false);
  return (
    <StepCard step={step} note="Hàng hóa cần có trước khi nhập tồn kho đầu kỳ">
      <p>Thêm từng mặt hàng với đơn vị tính, giá nhập và giá bán. Thuộc tính, quy đổi đơn vị và nhà cung cấp chỉnh tiếp ở Danh mục.</p>
      <ButtonRow>
        <Button variant="navy" onClick={() => setAdding(true)}>
          {CATALOG_LABELS.addProduct}
        </Button>
        <TextLink to="/catalog/products">{CATALOG_LABELS.products}</TextLink>
        <TextLink to="/catalog/suppliers">{CATALOG_LABELS.suppliers}</TextLink>
      </ButtonRow>
      {adding ? <QuickProductDialog onClose={() => setAdding(false)} /> : null}
    </StepCard>
  );
}
