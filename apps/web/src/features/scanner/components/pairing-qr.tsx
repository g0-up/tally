import { useMemo } from "react";
import { encode } from "uqr";

export const PAIRING_QR_LABEL = "Mã QR ghép đôi điện thoại";

/** Modules of empty margin around the code; cameras need four to find it. */
const QUIET_ZONE = 4;

/** One square per dark module, drawn as a single path. */
function modulesPath(data: readonly (readonly boolean[])[]): string {
  let d = "";
  data.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) d += `M${x} ${y}h1v1h-1z`;
    }),
  );
  return d;
}

/** The QR code a phone scans to pair: dark modules on a light ground, whatever the page theme. */
export function PairingQr({ value }: { value: string }) {
  const qr = useMemo(() => {
    const code = encode(value, { border: QUIET_ZONE, ecc: "M" });
    return { size: code.size, path: modulesPath(code.data) };
  }, [value]);
  return (
    <svg className="pair-qr" role="img" aria-label={PAIRING_QR_LABEL} viewBox={`0 0 ${qr.size} ${qr.size}`} shapeRendering="crispEdges">
      <rect className="pair-qr-bg" width={qr.size} height={qr.size} />
      <path className="pair-qr-fg" d={qr.path} />
    </svg>
  );
}
