import type { components, operations } from "../../../shared/api/schema";

// OpenAPI types the settings response schemas are checked against with `satisfies`, so a renamed
// or retyped field breaks the typecheck. A schema that reads only some fields is checked against
// the contract without the ones it leaves out (`Omit`).

export type S = components["schemas"];

type Json<R> = R extends { content: { "application/json": infer B } } ? B : never;

/** The JSON body of an operation's 200 or 201 answer. */
export type ResponseOf<K extends keyof operations> = operations[K] extends { responses: infer R }
  ? R extends { 200: infer A }
    ? Json<A>
    : R extends { 201: infer B }
      ? Json<B>
      : never
  : never;
