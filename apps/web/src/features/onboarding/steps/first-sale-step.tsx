import { ButtonRow } from "../../../shared/ui/button";
import { TextLink } from "../../../shared/ui/link-button";
import { StepCard } from "../components/step-card";
import type { ChecklistStep } from "../steps";

export const POS_PATH = "/sales/pos";
export const OPEN_POS = "Mở POS bán hàng";

/** First sale: completes by itself with the first POS invoice (HD-); it can only be skipped by hand. */
export function FirstSaleStep({ step }: { step: ChecklistStep }) {
  return (
    <StepCard step={step} note="Tự hoàn tất khi có hóa đơn bán đầu tiên từ POS">
      <p>Bán thử một đơn ở POS để kiểm tra giá, tồn kho và in hóa đơn. Bước này tự đánh dấu xong khi hóa đơn đầu tiên được ghi sổ.</p>
      <ButtonRow>
        <TextLink to={POS_PATH}>{OPEN_POS}</TextLink>
      </ButtonRow>
    </StepCard>
  );
}
