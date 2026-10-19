// Accent-insensitive search used by the combobox and filter inputs: "banh" matches "Bánh",
// "dau" matches "Đậu". Mirrors the prototype `norm` (NFD, strip combining marks, đ → d, lower case).

const COMBINING_MARKS = /[̀-ͯ]/g;

export function normalizeText(value: unknown): string {
  return String(value ?? "")
    .normalize("NFD")
    .replace(COMBINING_MARKS, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D")
    .toLowerCase();
}

/** Search tokens of a query; the "·" separator the labels use is not a token. */
export function searchTokens(query: string): string[] {
  return normalizeText(query)
    .split(/\s+/)
    .filter((t) => t !== "" && t !== "·");
}

/** True when every token of the query occurs in at least one of the fields. */
export function matchesAllTokens(query: string | readonly string[], ...fields: unknown[]): boolean {
  const tokens = typeof query === "string" ? searchTokens(query) : query;
  if (tokens.length === 0) return true;
  const haystack = normalizeText(fields.filter((f) => f !== null && f !== undefined).join(" "));
  return tokens.every((t) => haystack.includes(t));
}
