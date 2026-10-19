import { useEffect, useRef, useState, type ReactNode } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { applyAuthError } from "../lib/auth-errors";
import { emailSchema, type EmailValues } from "../schemas";
import { FormAlert } from "./form-alert";

export interface EmailRequestFormProps {
  /** Sends the e-mail; resolves when the API accepted the request. */
  send: (email: string) => Promise<void>;
  pending: boolean;
  submitLabel: string;
  pendingLabel: string;
  /** Shown once the request was accepted, whatever the e-mail (no account enumeration). */
  done: (email: string) => ReactNode;
}

/**
 * One e-mail field and a button, for the requests that mail a link ("Quên mật khẩu", "Tìm cửa
 * hàng"). The answer never says whether the e-mail has an account: every accepted request ends on
 * the same neutral confirmation.
 */
export function EmailRequestForm({ send, pending, submitLabel, pendingLabel, done }: EmailRequestFormProps) {
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  // The form is replaced on success, taking the focused button with it; focus moves to the message.
  const sentMessage = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (sentTo !== null) sentMessage.current?.focus();
  }, [sentTo]);
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<EmailValues>({ resolver: zodResolver(emailSchema), defaultValues: { email: "" } });

  const onSubmit = handleSubmit(async ({ email }) => {
    setFormError(null);
    try {
      const trimmed = email.trim();
      await send(trimmed);
      setSentTo(trimmed);
    } catch (e) {
      setFormError(applyAuthError(e, ["email"], setError));
    }
  });

  if (sentTo !== null) {
    return (
      <div ref={sentMessage} tabIndex={-1}>
        <Callout tone="ok" role="status">
          {done(sentTo)}
        </Callout>
      </div>
    );
  }

  return (
    <>
      <FormAlert message={formError} />
      <form className="auth-form" noValidate onSubmit={(e) => void onSubmit(e)}>
        <Field label="Email" error={errors.email?.message}>
          <Input type="email" autoComplete="email" autoFocus invalid={errors.email !== undefined} {...register("email")} />
        </Field>
        <Button variant="primary" type="submit" pending={pending} pendingLabel={pendingLabel}>
          {submitLabel}
        </Button>
      </form>
    </>
  );
}
