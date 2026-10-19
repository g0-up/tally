import { useEffect } from "react";
import { normalizeDocNo, registerDocOpener } from "../../shared/domain/doc-no";
import { toast } from "../../shared/ui/toast-store";

// Document numbers the sales screens have loaded, so a DocNo link ("SO-0042") can open its drawer:
// the API has no lookup by document number, and the drawers load by id. The sales overlay host
// registers the prefixes while it is mounted and receives the document to open.

export type SalesDocKind = "quote" | "invoice" | "stockOut" | "document" | "return" | "exchange";

export interface SalesDocRef {
  kind: SalesDocKind;
  id: string;
}

const index = new Map<string, SalesDocRef>();

/** Records the ids of loaded documents under their numbers. */
export function rememberDocs(kind: SalesDocKind, docs: readonly { id: string; doc_no: string }[]): void {
  for (const d of docs) index.set(normalizeDocNo(d.doc_no), { kind, id: d.id });
}

/** The loaded document a number refers to, or undefined when no screen has loaded it yet. */
export function lookupDoc(docNo: string): SalesDocRef | undefined {
  return index.get(normalizeDocNo(docNo));
}

/** Records documents whenever a list or detail query delivers them. */
export function useRememberDocs(kind: SalesDocKind, docs: readonly { id: string; doc_no: string }[] | null | undefined): void {
  useEffect(() => {
    if (docs) rememberDocs(kind, docs);
  }, [kind, docs]);
}

/** Prefixes of the documents the sales drawers show (POS sales are numbered HD, deliveries BH). */
export const SALES_DOC_PREFIXES = ["BG", "SO", "PX", "BH", "HD", "TH", "DH"] as const;

let host: ((doc: SalesDocRef) => void) | null = null;

function openByNumber(docNo: string): void {
  const doc = lookupDoc(docNo);
  if (!doc || host === null) {
    toast.error(`Chưa mở được ${docNo}: chứng từ chưa có trong danh sách đang xem.`);
    return;
  }
  host(doc);
}

/** Registers the sales prefixes with DocNo; returns the unregister function. Idempotent. */
export function registerSalesOpeners(): () => void {
  const off = SALES_DOC_PREFIXES.map((prefix) => registerDocOpener(prefix, openByNumber));
  return () => {
    for (const f of off) f();
  };
}

/** The overlay host that opens drawers for DocNo links; null when it unmounts. */
export function setSalesDocHost(open: ((doc: SalesDocRef) => void) | null): void {
  host = open;
}

/** Test helper: forget every remembered document. */
export function clearDocIndex(): void {
  index.clear();
}
