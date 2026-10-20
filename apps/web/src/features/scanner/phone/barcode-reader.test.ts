import { describe, expect, it, vi } from "vitest";
import { SCAN_FORMATS, createBarcodeReader, type DetectorClass } from "./barcode-reader";

/** A BarcodeDetector class that reads `formats` and answers every frame with `codes`. */
function detectorClass(formats: readonly string[] | Error, codes: string[] = []) {
  const made: string[][] = [];
  class Fake {
    constructor({ formats: wanted }: { formats: string[] }) {
      made.push(wanted);
    }
    static getSupportedFormats() {
      return formats instanceof Error ? Promise.reject(formats) : Promise.resolve(formats);
    }
    detect() {
      return Promise.resolve(codes.map((rawValue) => ({ rawValue })));
    }
  }
  return { cls: Fake as unknown as DetectorClass, made };
}

const video = () => document.createElement("video");

describe("createBarcodeReader", () => {
  it("uses the browser's detector when it reads all six formats", async () => {
    const native = detectorClass([...SCAN_FORMATS, "pdf417"], ["8934563138165"]);
    const loadWasm = vi.fn();
    const reader = await createBarcodeReader({ native: native.cls, loadWasm });
    expect(reader.kind).toBe("native");
    expect(loadWasm).not.toHaveBeenCalled();
    expect(native.made).toEqual([[...SCAN_FORMATS]]);
    expect(await reader.detect(video())).toEqual(["8934563138165"]);
  });

  it("loads the WASM reader when the browser's misses a format", async () => {
    const native = detectorClass(SCAN_FORMATS.filter((f) => f !== "upc_e"));
    const wasm = detectorClass(SCAN_FORMATS, ["0012345678905"]);
    const reader = await createBarcodeReader({ native: native.cls, loadWasm: () => Promise.resolve(wasm.cls) });
    expect(reader.kind).toBe("wasm");
    expect(native.made).toEqual([]);
    expect(await reader.detect(video())).toEqual(["0012345678905"]);
  });

  it("loads the WASM reader when the browser has no detector or cannot list its formats", async () => {
    const wasm = detectorClass(SCAN_FORMATS);
    expect((await createBarcodeReader({ native: null, loadWasm: () => Promise.resolve(wasm.cls) })).kind).toBe("wasm");
    const broken = detectorClass(new Error("not allowed"));
    expect((await createBarcodeReader({ native: broken.cls, loadWasm: () => Promise.resolve(wasm.cls) })).kind).toBe("wasm");
  });

  it("reports a WASM reader that failed to load", async () => {
    await expect(createBarcodeReader({ native: null, loadWasm: () => Promise.reject(new Error("wasm blocked")) })).rejects.toThrow("wasm blocked");
  });
});
