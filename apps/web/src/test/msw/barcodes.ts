import { HttpResponse, http } from "msw";
import { apiError, apiUrl } from "./handlers";
import { server } from "./server";

// A stateful stand-in for the barcode routes of SKUs, installed per test with `barcodeServer()`.
// GET lists what the store holds now; PATCH moves a barcode to the unit in its body and logs the
// call, so tests assert on what was sent. Single answers can be overridden (held, refused).

export interface BarcodeRow {
  id: string;
  unit_id: string;
  code: string;
  created_at: string;
}

export interface BarcodePatchCall {
  skuId: string;
  barcodeId: string;
  body: unknown;
}

/** An answer to use instead of the fixture's, or undefined to keep it. */
type PatchOverride = ((call: BarcodePatchCall) => Response | undefined | Promise<Response | undefined>) | null;

export interface BarcodeServer {
  /** Barcodes by SKU id, as the store holds them now. */
  readonly barcodes: Map<string, BarcodeRow[]>;
  /** PATCH calls in the order they arrived. */
  readonly patches: BarcodePatchCall[];
  /** Answers PATCH differently while `fn` returns a response. */
  onPatch(fn: PatchOverride): void;
}

/** A barcode row as the API sends it, created at a fixed time. */
export function barcodeRow(id: string, code: string, unitId: string): BarcodeRow {
  return { id, unit_id: unitId, code, created_at: "2026-10-01T02:00:00Z" };
}

const param = (v: string | readonly string[] | undefined): string => (typeof v === "string" ? v : "");

export function barcodeServer(initial: Record<string, BarcodeRow[]> = {}): BarcodeServer {
  const barcodes = new Map(Object.entries(initial).map(([sku, rows]) => [sku, rows.map((r) => ({ ...r }))]));
  const patches: BarcodePatchCall[] = [];
  let override: PatchOverride = null;

  server.use(
    http.get(apiUrl("/skus/:id/barcodes"), ({ params }) => HttpResponse.json({ data: barcodes.get(param(params["id"])) ?? [], next_cursor: null })),
    http.patch(apiUrl("/skus/:id/barcodes/:barcodeId"), async ({ params, request }) => {
      const call: BarcodePatchCall = { skuId: param(params["id"]), barcodeId: param(params["barcodeId"]), body: await request.json() };
      patches.push(call);
      const answer = await override?.(call);
      if (answer) return answer;
      const row = barcodes.get(call.skuId)?.find((b) => b.id === call.barcodeId);
      if (!row) return apiError(404, "not_found");
      const unitId = (call.body as { unit_id?: unknown } | null)?.unit_id;
      if (typeof unitId !== "string" || unitId === "") return apiError(422, "validation_failed", { fields: { unit_id: "required" } });
      row.unit_id = unitId;
      return HttpResponse.json(row);
    }),
  );

  return {
    barcodes,
    patches,
    onPatch: (fn) => {
      override = fn;
    },
  };
}
