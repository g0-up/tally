import { createContext, useContext } from "react";

/** What a `Field` tells the control inside it: the ids of its hint and error, and whether it has an error. */
export interface FieldControlState {
  describedBy: string | undefined;
  invalid: boolean;
}

export const FieldContext = createContext<FieldControlState | null>(null);

/** Joins description ids, dropping empty ones; undefined when there is none. */
export function joinIds(...ids: ReadonlyArray<string | undefined | null | false>): string | undefined {
  const joined = ids.filter((id): id is string => typeof id === "string" && id !== "").join(" ");
  return joined === "" ? undefined : joined;
}

/**
 * Props a form control merges into its own so the enclosing `Field`'s hint and error are read with
 * it (`aria-describedby`) and its error state is exposed (`aria-invalid`). Outside a Field it
 * returns the control's own values unchanged.
 */
export function useFieldControl(own: { describedBy?: string | undefined; invalid?: boolean | undefined }): {
  "aria-describedby": string | undefined;
  "aria-invalid": true | undefined;
} {
  const field = useContext(FieldContext);
  return {
    "aria-describedby": joinIds(own.describedBy, field?.describedBy),
    "aria-invalid": own.invalid || field?.invalid ? true : undefined,
  };
}
