// UUIDv7 (RFC 9562): 48-bit Unix milliseconds, version 7, then random bits. An offline sale gets
// one on the device; time-ordered ids keep the server's unique index on offline_client_id compact
// and sort the exception queue the way the sales were made.

/** A new UUIDv7 for the instant `now` (ms); `random` fills the 10 random bytes (tests pass their own). */
export function uuidv7(now: number = Date.now(), random: (bytes: Uint8Array<ArrayBuffer>) => void = (b) => crypto.getRandomValues(b)): string {
  const b = new Uint8Array(new ArrayBuffer(16));
  random(b.subarray(6));
  let ms = Math.max(0, Math.floor(now));
  for (let i = 5; i >= 0; i--) {
    b[i] = ms % 256;
    ms = Math.floor(ms / 256);
  }
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x70;
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

const UUIDV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function isUuidv7(value: string): boolean {
  return UUIDV7.test(value);
}

/** Unix milliseconds carried by a UUIDv7. */
export function uuidv7Time(value: string): number {
  return parseInt(value.replace(/-/g, "").slice(0, 12), 16);
}
