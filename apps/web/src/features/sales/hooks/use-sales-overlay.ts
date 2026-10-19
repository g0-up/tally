import { createContext, useContext } from "react";
import type { Invoice, Quote, SalesReturn } from "../schemas";

// One drawer and one dialog at a time across the sales screens: a dialog opened from a drawer
// (deposit, cancel, delivery) sits on top of it and the drawer stays when the dialog closes.

export type SalesDrawer =
  | { kind: "quote"; id: string }
  | { kind: "invoice"; id: string }
  | { kind: "stockOut"; id: string }
  | { kind: "document"; id: string }
  | { kind: "return"; id: string }
  | { kind: "exchange"; id: string };

export type SalesDialog =
  | { kind: "quoteForm"; mode: "new" }
  | { kind: "quoteForm"; mode: "edit" | "revise"; quote: Quote }
  | { kind: "quoteReject"; quote: Quote }
  | { kind: "invoiceForm"; invoice: Invoice | null }
  | { kind: "invoiceDeposit"; invoice: Invoice }
  | { kind: "invoiceCancel"; invoice: Invoice }
  | { kind: "invoiceDelete"; invoice: Invoice }
  | { kind: "stockOutForm"; invoice: Invoice }
  | { kind: "return"; documentId: string | null }
  | { kind: "exchange"; documentId: string | null }
  | { kind: "refund"; salesReturn: SalesReturn };

export interface SalesOverlay {
  openDrawer: (drawer: SalesDrawer) => void;
  closeDrawer: () => void;
  openDialog: (dialog: SalesDialog) => void;
  closeDialog: () => void;
}

export const SalesOverlayContext = createContext<SalesOverlay | null>(null);

export function useSalesOverlay(): SalesOverlay {
  const overlay = useContext(SalesOverlayContext);
  if (overlay === null) throw new Error("useSalesOverlay must be used inside SalesOverlayHost");
  return overlay;
}
