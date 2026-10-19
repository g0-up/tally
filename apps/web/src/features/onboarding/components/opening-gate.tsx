import type { ReactNode } from "react";
import { Callout } from "../../../shared/ui/callout";
import { LinkButton } from "../../../shared/ui/link-button";

export const CUTOFF_MISSING = "Chọn ngày chốt số dư đầu kỳ trong Thông tin cửa hàng trước.";
export const OPENINGS_CONFIRMED = "Số dư đầu kỳ đã xác nhận, không sửa được nữa.";

/**
 * An opening balance is entered as of the cutoff date and stops changing once confirmed: the form
 * shows only between those two moments.
 */
export function OpeningGate({ cutoff, confirmed, onOpenProfile, children }: { cutoff: string | null; confirmed: boolean; onOpenProfile: () => void; children: (cutoff: string) => ReactNode }) {
  if (confirmed) return <Callout tone="ok">{OPENINGS_CONFIRMED}</Callout>;
  if (cutoff === null) {
    return (
      <Callout tone="warn">
        {CUTOFF_MISSING} <LinkButton onClick={onOpenProfile}>Mở Thông tin cửa hàng</LinkButton>
      </Callout>
    );
  }
  return <>{children(cutoff)}</>;
}
