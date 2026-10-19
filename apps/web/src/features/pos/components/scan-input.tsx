import type { Ref } from "react";
import { Button } from "../../../shared/ui/button";

export interface ScanInputProps {
  value: string;
  onChange: (value: string) => void;
  /** Enter or "Thêm": add the first match. */
  onSubmit: () => void;
  ref?: Ref<HTMLInputElement>;
}

/** Scan / search field: a barcode scanner types the code and presses Enter. F2 focuses it. */
export function ScanInput({ value, onChange, onSubmit, ref }: ScanInputProps) {
  return (
    <div className="scan">
      <input
        ref={ref}
        value={value}
        placeholder="Mã vạch, mã hàng hoặc tên hàng…"
        autoComplete="off"
        aria-label="Tìm hoặc quét hàng"
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            onSubmit();
          }
        }}
      />
      <Button variant="navy" onClick={onSubmit}>
        Thêm
      </Button>
    </div>
  );
}
