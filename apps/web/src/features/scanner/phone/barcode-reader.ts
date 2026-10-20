// Reads barcodes from the camera's video. The browser's own BarcodeDetector is used when it reads
// all six formats the shop needs (Chrome on Android); otherwise (Safari has none, some desktop
// Chromes lack formats) the ZXing WebAssembly ponyfill is loaded on demand. Its script and .wasm
// are served by this site, never a CDN, and stay out of the service worker's precache: only the
// scan page ever downloads them. Codes come back exactly as read (the WASM reader gives UPC-A as
// 13 digits); the server matches GTIN variants, so nothing is normalised here.

export const SCAN_FORMATS = ["ean_13", "ean_8", "upc_a", "upc_e", "code_128", "qr_code"] as const;

/** What a BarcodeDetector (the browser's or the ponyfill) offers. */
export interface DetectorLike {
  detect(source: HTMLVideoElement): Promise<ReadonlyArray<{ rawValue: string }>>;
}

export interface DetectorClass {
  new (options: { formats: string[] }): DetectorLike;
  getSupportedFormats(): Promise<readonly string[]>;
}

export interface BarcodeReader {
  readonly kind: "native" | "wasm";
  /** The codes visible in the current video frame, raw. */
  detect(source: HTMLVideoElement): Promise<string[]>;
}

/** The browser's BarcodeDetector, when it has one. */
export function nativeDetectorClass(): DetectorClass | null {
  const candidate = (globalThis as { BarcodeDetector?: unknown }).BarcodeDetector;
  if (typeof candidate !== "function") return null;
  const cls = candidate as Partial<DetectorClass>;
  return typeof cls.getSupportedFormats === "function" ? (candidate as DetectorClass) : null;
}

/** True when the class reads every format in SCAN_FORMATS; a failing query counts as no. */
export async function readsAllFormats(cls: DetectorClass): Promise<boolean> {
  try {
    const supported = new Set(await cls.getSupportedFormats());
    return SCAN_FORMATS.every((f) => supported.has(f));
  } catch {
    return false;
  }
}

/**
 * The ZXing ponyfill, ready to read. Both its code and the URL of its .wasm are imported lazily so
 * they land in the `barcode-reader` chunk, and the module is prepared before first use, pointed at
 * the self-hosted .wasm, so a blocked or missing file fails here, where it can be reported.
 */
export async function loadWasmDetectorClass(): Promise<DetectorClass> {
  const [{ BarcodeDetector, prepareZXingModule }, { default: wasmUrl }] = await Promise.all([
    import("barcode-detector/ponyfill"),
    import("zxing-wasm/reader/zxing_reader.wasm?url"),
  ]);
  await prepareZXingModule({
    overrides: { locateFile: (file: string, prefix: string) => (file.endsWith(".wasm") ? wasmUrl : prefix + file) },
    fireImmediately: true,
  });
  return BarcodeDetector as unknown as DetectorClass;
}

export interface CreateReaderOptions {
  native?: DetectorClass | null;
  loadWasm?: () => Promise<DetectorClass>;
}

function readerFrom(kind: BarcodeReader["kind"], cls: DetectorClass): BarcodeReader {
  const detector = new cls({ formats: [...SCAN_FORMATS] });
  return {
    kind,
    detect: async (source) => (await detector.detect(source)).map((d) => d.rawValue),
  };
}

/** The native reader when it covers every format, else the WASM one (loaded on first call). */
export async function createBarcodeReader({ native = nativeDetectorClass(), loadWasm = loadWasmDetectorClass }: CreateReaderOptions = {}): Promise<BarcodeReader> {
  if (native !== null && (await readsAllFormats(native))) return readerFrom("native", native);
  return readerFrom("wasm", await loadWasm());
}
