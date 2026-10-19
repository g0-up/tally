/** Field errors of an onboarding form, by field key. */
export type FieldErrors = Readonly<Record<string, string>>;

/** Ids a control points at: its error under it, when shown. */
export const errRef = (id: string, error: string | undefined) => (error ? `${id}-e` : undefined);

/** The errors without the one of a field the owner just edited (prototype: typing clears its error). */
export function withoutError(errors: FieldErrors, key: string): FieldErrors {
  if (!(key in errors)) return errors;
  return Object.fromEntries(Object.entries(errors).filter(([k]) => k !== key));
}
