import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Button } from "../../../shared/ui/button";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { recoveryCodeSchema, totpCodeSchema, type RecoveryCodeValues, type TotpCodeValues } from "../schemas";

/** Sends a code; resolves with a message for the code field (wrong code) or null. */
type SubmitCode<B> = (body: B) => Promise<string | null>;

interface CodeFormProps<B> {
  pending: boolean;
  onSubmit: SubmitCode<B>;
}

/** 6-digit TOTP code from the authenticator app. A wrong code clears the field for the next one. */
export function TotpCodeForm({ pending, onSubmit }: CodeFormProps<{ code: string }>) {
  const {
    register,
    handleSubmit,
    setError,
    resetField,
    setFocus,
    formState: { errors },
  } = useForm<TotpCodeValues>({ resolver: zodResolver(totpCodeSchema), defaultValues: { code: "" } });

  const submit = handleSubmit(async (values) => {
    const message = await onSubmit({ code: values.code.replace(/\s+/g, "") });
    if (message) {
      resetField("code");
      setError("code", { type: "server", message });
      setFocus("code");
    }
  });

  return (
    <form className="auth-form" noValidate onSubmit={(e) => void submit(e)}>
      <Field label="Mã xác thực" error={errors.code?.message}>
        <Input inputMode="numeric" autoComplete="one-time-code" maxLength={7} autoFocus invalid={errors.code !== undefined} {...register("code")} />
      </Field>
      <Button variant="primary" type="submit" pending={pending} pendingLabel="Đang xác thực…">
        Xác thực
      </Button>
    </form>
  );
}

/** One-time recovery code saved when TOTP was turned on. */
export function RecoveryCodeForm({ pending, onSubmit }: CodeFormProps<{ recovery_code: string }>) {
  const {
    register,
    handleSubmit,
    setError,
    setFocus,
    formState: { errors },
  } = useForm<RecoveryCodeValues>({ resolver: zodResolver(recoveryCodeSchema), defaultValues: { recovery_code: "" } });

  const submit = handleSubmit(async (values) => {
    const message = await onSubmit({ recovery_code: values.recovery_code.trim() });
    if (message) {
      setError("recovery_code", { type: "server", message });
      setFocus("recovery_code");
    }
  });

  return (
    <form className="auth-form" noValidate onSubmit={(e) => void submit(e)}>
      <Field label="Mã khôi phục" error={errors.recovery_code?.message}>
        <Input autoComplete="off" autoCapitalize="none" spellCheck={false} autoFocus invalid={errors.recovery_code !== undefined} {...register("recovery_code")} />
      </Field>
      <Button variant="primary" type="submit" pending={pending} pendingLabel="Đang xác thực…">
        Xác thực
      </Button>
    </form>
  );
}
