// The address in the pairing QR code. The claim code rides in the fragment, which browsers never
// send to a server or put in a Referer, and the phone's scan page reads it from `#c=`.

export const CLAIM_HASH_KEY = "c";

export function pairingUrl(claimCode: string, origin: string = window.location.origin): string {
  return `${origin}/scan#${CLAIM_HASH_KEY}=${encodeURIComponent(claimCode)}`;
}
