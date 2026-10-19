import type { UseFormRegisterReturn } from "react-hook-form";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";

export interface NewPasswordFieldsProps {
  password: UseFormRegisterReturn;
  confirm: UseFormRegisterReturn;
  passwordError: string | undefined;
  confirmError: string | undefined;
  /** Focus the first field when the screen opens. */
  autoFocus?: boolean;
}

/** New password typed twice, as on sign-up, password reset and first-password verification. */
export function NewPasswordFields({ password, confirm, passwordError, confirmError, autoFocus = false }: NewPasswordFieldsProps) {
  return (
    <>
      <Field label="Mật khẩu mới" hint="Ít nhất 8 ký tự." error={passwordError}>
        <Input type="password" autoComplete="new-password" autoFocus={autoFocus} invalid={passwordError !== undefined} {...password} />
      </Field>
      <Field label="Nhập lại mật khẩu" error={confirmError}>
        <Input type="password" autoComplete="new-password" invalid={confirmError !== undefined} {...confirm} />
      </Field>
    </>
  );
}
