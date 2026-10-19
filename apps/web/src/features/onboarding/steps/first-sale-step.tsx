import { useNavigate } from "react-router";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { LinkButton } from "../../../shared/ui/link-button";
import { useOnboardingScreen } from "../onboarding-context";

const POS_PATH = "/sales/pos";

/** Prototype `OB_BODY.first_sale`: completes by itself with the first POS invoice; opens once the balances are locked. */
export function FirstSaleStep() {
  const { steps, confirmed, cta, goTo } = useOnboardingScreen();
  const navigate = useNavigate();
  if (steps.find((s) => s.key === "first_sale")?.status === "completed") {
    return (
      <Callout tone="ok" role="note">
        <b>Đã có hóa đơn bán đầu tiên.</b> Cửa hàng đang hoạt động bình thường.
      </Callout>
    );
  }
  if (!confirmed) {
    return (
      <Callout role="note">
        <b>POS mở sau khi xác nhận số dư đầu kỳ.</b> Bán hàng trước khi khóa sổ sẽ làm lệch tồn kho và tiền đầu kỳ.{" "}
        <LinkButton onClick={() => goTo("confirm_opening_balances")}>Đến bước Xác nhận</LinkButton>
      </Callout>
    );
  }
  return (
    <>
      <ol className="onb-check">
        <li>Mở POS, quét hoặc tìm một mặt hàng đã nhập tồn.</li>
        <li>Thu tiền vào quỹ và in hóa đơn.</li>
        <li>Quay lại đây: bước này tự đánh dấu xong.</li>
      </ol>
      <ButtonRow>
        <Button variant={cta} onClick={() => void navigate(POS_PATH)}>
          Mở POS bán hàng
        </Button>
      </ButtonRow>
    </>
  );
}
