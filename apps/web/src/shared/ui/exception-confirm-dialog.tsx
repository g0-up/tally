import { useCallback, useRef, useState, type ReactNode } from "react";
import { useApiClient } from "../api/api-context";
import { errorMessage } from "../api/api-error";
import { PATHS, exceptionConfirmationSchema } from "../api/endpoints";
import {
  ConfirmExceptionContext,
  EXCEPTION_CHECKBOX,
  EXCEPTION_SUB,
  EXCEPTION_SUBMIT,
  defaultExceptionIssues,
  exceptionUncheckedMessage,
  type ConfirmException,
  type ExceptionIssue,
  type ExceptionPrompt,
} from "../hooks/use-confirm-exception";
import { payloadHash, randomId } from "../lib/canonical-hash";
import { Callout } from "./callout";
import { Checkbox } from "./checkbox";
import { CommitDialog } from "./commit-dialog";
import { toast } from "./toast-store";

export interface ExceptionConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  /** Called only when the box is ticked. */
  onConfirm: () => void;
  title: ReactNode;
  sub?: ReactNode;
  issues: readonly ExceptionIssue[];
  checkboxLabel?: string;
  submitLabel?: string;
  pending?: boolean;
}

/**
 * Prototype `soConfirm`: warn callout listing "<b>{type}:</b> {detail}" per issue, a confirmation
 * checkbox and a submit that refuses to go on while the box is unticked. Title, sub and checkbox
 * text are props so disposal, count variance and other exception kinds reuse it.
 */
export function ExceptionConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  sub = EXCEPTION_SUB,
  issues,
  checkboxLabel = EXCEPTION_CHECKBOX,
  submitLabel = EXCEPTION_SUBMIT,
  pending = false,
}: ExceptionConfirmDialogProps) {
  const [checked, setChecked] = useState(false);
  return (
    <CommitDialog
      open={open}
      onClose={onClose}
      title={title}
      sub={sub}
      submitLabel={submitLabel}
      pending={pending}
      onSubmit={() => {
        if (!checked) toast.commitError(exceptionUncheckedMessage(checkboxLabel));
        else onConfirm();
      }}
    >
      {issues.length ? (
        <Callout tone="warn">
          {issues.map((it, i) => (
            <span key={`${it.type}-${i}`}>
              {i > 0 ? <br /> : null}
              <b>{it.type}:</b> {it.detail}
            </span>
          ))}
        </Callout>
      ) : null}
      <Checkbox label={checkboxLabel} checked={checked} onChange={(e) => setChecked(e.target.checked)} />
    </CommitDialog>
  );
}

/** The API hashes the command body without the confirmation reference, so the client does too. */
function withoutConfirmation(payload: unknown): unknown {
  if (typeof payload !== "object" || payload === null || Array.isArray(payload)) return payload;
  const { exception_confirmation_id: _ignored, ...rest } = payload as Record<string, unknown>;
  return rest;
}

interface Active {
  id: number;
  prompt: ExceptionPrompt;
}

/**
 * Mounts the shared exception dialog. `useConfirmException()(prompt)` opens it; confirming stores
 * the confirmation (POST /exception-confirmations, own Idempotency-Key per confirmation) and
 * resolves with its id, cancelling resolves null.
 */
export function ExceptionConfirmProvider({ children }: { children: ReactNode }) {
  const client = useApiClient();
  const [active, setActive] = useState<Active | null>(null);
  const [pending, setPending] = useState(false);
  const resolveRef = useRef<((id: string | null) => void) | null>(null);
  const keyRef = useRef<string | null>(null);
  const seq = useRef(0);

  const settle = useCallback((id: string | null) => {
    const resolve = resolveRef.current;
    resolveRef.current = null;
    keyRef.current = null;
    setActive(null);
    resolve?.(id);
  }, []);

  const confirm = useCallback<ConfirmException>((prompt) => {
    // A second prompt replaces the first one, which counts as cancelled.
    resolveRef.current?.(null);
    keyRef.current = null;
    return new Promise<string | null>((resolve) => {
      resolveRef.current = resolve;
      seq.current += 1;
      setActive({ id: seq.current, prompt });
    });
  }, []);

  const submit = async () => {
    if (!active) return;
    const { kind, subject, payload } = active.prompt;
    keyRef.current ??= randomId();
    setPending(true);
    try {
      const hash = await payloadHash(withoutConfirmation(payload));
      const res = await client.post(PATHS.exceptionConfirmations, { kind, ...subject, payload_hash: hash }, { idempotencyKey: keyRef.current, schema: exceptionConfirmationSchema });
      settle(res.id);
    } catch (e) {
      toast.commitError(errorMessage(e));
    } finally {
      setPending(false);
    }
  };

  const prompt = active?.prompt;
  return (
    <ConfirmExceptionContext.Provider value={confirm}>
      {children}
      {prompt ? (
        <ExceptionConfirmDialog
          key={active.id}
          open
          onClose={() => settle(null)}
          onConfirm={() => void submit()}
          title={prompt.title}
          {...(prompt.sub !== undefined ? { sub: prompt.sub } : {})}
          issues={prompt.issues ?? defaultExceptionIssues(prompt.kind)}
          {...(prompt.checkboxLabel !== undefined ? { checkboxLabel: prompt.checkboxLabel } : {})}
          {...(prompt.submitLabel !== undefined ? { submitLabel: prompt.submitLabel } : {})}
          pending={pending}
        />
      ) : null}
    </ConfirmExceptionContext.Provider>
  );
}
