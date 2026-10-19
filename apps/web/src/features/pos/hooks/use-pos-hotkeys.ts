import { useHotkeys } from "../../../shared/hooks/use-hotkeys";

export interface PosHotkeys {
  /** F2: focus the scan field. */
  focusScan: () => void;
  /** F4: "Lưu giỏ" / "Lưu thay đổi". */
  hold: () => void;
  /** F9: "Thanh toán". */
  pay: () => void;
}

/**
 * The POS shortcuts, bound only while the POS page is mounted; the shared hotkey registry already
 * suspends them while a dialog or drawer is open.
 */
export function usePosHotkeys({ focusScan, hold, pay }: PosHotkeys): void {
  useHotkeys({ F2: focusScan, F4: hold, F9: pay });
}
