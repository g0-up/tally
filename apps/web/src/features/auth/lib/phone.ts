// Vietnamese phone numbers as the API accepts them (identity normalizePhone): the domestic form
// "0912418226" of a mobile (0 + 9 digits, network prefix 3, 5, 7, 8 or 9) or a landline
// (02 + 9 digits), typed with or without the country code (+84, 0084, 84) and with spaces, dots,
// dashes or brackets. The API stays the authority; this only catches typos before a request.

export const MAX_PHONE_LENGTH = 30;

const ALLOWED = /^\+?[\d .\-()]+$/;

/** Digits with the country code folded to the domestic leading 0, like the API's PhoneKey. */
function phoneKey(v: string): string {
  const digits = v.replace(/\D/g, "");
  let national: string;
  if (v.startsWith("+84")) national = digits.slice(2);
  else if (digits.startsWith("0084")) national = digits.slice(4);
  else if (digits.startsWith("84") && digits.length === 11) national = digits.slice(2);
  else return digits;
  return `0${national.replace(/^0/, "")}`;
}

/** Domestic form of a Vietnamese phone number, or null when the input is not one. */
export function normalizeVnPhone(raw: string): string | null {
  const v = raw.trim();
  if (v === "" || v.length > MAX_PHONE_LENGTH || !ALLOWED.test(v)) return null;
  const key = phoneKey(v);
  if (!key.startsWith("0")) return null;
  const prefix = key.charAt(1);
  if ("35789".includes(prefix) && prefix !== "") return key.length === 10 ? key : null;
  if (prefix === "2") return key.length === 11 ? key : null;
  return null;
}
