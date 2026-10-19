import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { toast } from "../../../shared/ui/toast-store";
import type { FieldErrors } from "../components/field-errors";

/**
 * Prototype `obFail`: shows every error under its field, moves focus to the first one and says how
 * many are left to fix.
 */
export function useOnbFail(setErrors: (errors: FieldErrors) => void): (errors: FieldErrors) => void {
  const focusFirstInvalid = useFocusFirstInvalid();
  return (errors) => {
    setErrors(errors);
    focusFirstInvalid();
    toast.error(
      <>
        <b>Chưa lưu.</b> Còn {Object.keys(errors).length} chỗ cần sửa, đã đưa con trỏ tới chỗ đầu tiên.
      </>,
    );
  };
}
