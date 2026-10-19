import { useLayoutEffect, useMemo, useState, type ReactNode } from "react";
import { useCustomerDirectory } from "../api/queries";
import { ExchangeDrawer } from "../components/exchange-drawer";
import { InvoiceDrawer } from "../components/invoice-drawer";
import { QuoteDrawer } from "../components/quote-drawer";
import { ReturnDrawer } from "../components/return-drawer";
import { SalesDocumentDrawer } from "../components/sales-document-drawer";
import { StockOutDrawer } from "../components/stock-out-drawer";
import { registerSalesOpeners, setSalesDocHost, type SalesDocRef } from "../doc-index";
import { SalesOverlayContext, type SalesDialog, type SalesDrawer, type SalesOverlay } from "../hooks/use-sales-overlay";
import { ExchangeDialog } from "./exchange-dialog";
import { InvoiceCancelDialog } from "./invoice-cancel-dialog";
import { InvoiceDeleteDialog } from "./invoice-delete-dialog";
import { InvoiceDepositDialog } from "./invoice-deposit-dialog";
import { InvoiceDialog } from "./invoice-dialog";
import { QuoteDialog } from "./quote-dialog";
import { QuoteRejectDialog } from "./quote-reject-dialog";
import { RefundDialog } from "./refund-dialog";
import { ReturnDialog } from "./return-dialog";
import { StockOutDialog } from "./stock-out-dialog";

/**
 * The drawer and dialog layer of a sales screen (quotes, invoices, returns, POS). Document numbers
 * shown anywhere on the screen open their drawer here; a dialog opened from a drawer sits on top.
 */
export function SalesOverlayHost({ children }: { children: ReactNode }) {
  const [drawer, setDrawer] = useState<SalesDrawer | null>(null);
  const [dialog, setDialog] = useState<SalesDialog | null>(null);
  const customers = useCustomerDirectory();

  // DocNo links render in the first pass, so the prefixes are registered before it; the effect
  // owns the registration afterwards and removes it when the screen unmounts.
  useState(registerSalesOpeners);
  useLayoutEffect(() => registerSalesOpeners(), []);
  useLayoutEffect(() => {
    setSalesDocHost((doc: SalesDocRef) => setDrawer({ kind: doc.kind, id: doc.id }));
    return () => setSalesDocHost(null);
  }, []);

  const overlay = useMemo<SalesOverlay>(
    () => ({
      openDrawer: setDrawer,
      closeDrawer: () => setDrawer(null),
      openDialog: setDialog,
      closeDialog: () => setDialog(null),
    }),
    [],
  );
  const closeDrawer = overlay.closeDrawer;
  const closeDialog = overlay.closeDialog;

  return (
    <SalesOverlayContext.Provider value={overlay}>
      {children}
      {drawer?.kind === "quote" ? <QuoteDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}
      {drawer?.kind === "invoice" ? <InvoiceDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}
      {drawer?.kind === "stockOut" ? <StockOutDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}
      {drawer?.kind === "document" ? <SalesDocumentDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}
      {drawer?.kind === "return" ? <ReturnDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}
      {drawer?.kind === "exchange" ? <ExchangeDrawer key={drawer.id} id={drawer.id} onClose={closeDrawer} /> : null}

      {dialog?.kind === "quoteForm" ? (
        <QuoteDialog
          mode={dialog.mode}
          quote={dialog.mode === "new" ? null : dialog.quote}
          onClose={closeDialog}
          onSaved={(q) => {
            closeDialog();
            setDrawer({ kind: "quote", id: q.id });
          }}
        />
      ) : null}
      {dialog?.kind === "quoteReject" ? <QuoteRejectDialog quote={dialog.quote} customerName={customers.name(dialog.quote.customer_party_id)} onClose={closeDialog} /> : null}
      {dialog?.kind === "invoiceForm" ? (
        <InvoiceDialog
          invoice={dialog.invoice}
          onClose={closeDialog}
          onSaved={(invoice) => {
            closeDialog();
            setDrawer({ kind: "invoice", id: invoice.id });
          }}
        />
      ) : null}
      {dialog?.kind === "invoiceDeposit" ? <InvoiceDepositDialog invoice={dialog.invoice} onClose={closeDialog} /> : null}
      {dialog?.kind === "invoiceCancel" ? <InvoiceCancelDialog invoice={dialog.invoice} onClose={closeDialog} /> : null}
      {dialog?.kind === "invoiceDelete" ? (
        <InvoiceDeleteDialog
          invoice={dialog.invoice}
          customerName={customers.name(dialog.invoice.customer_party_id)}
          onClose={closeDialog}
          onDeleted={() => {
            closeDialog();
            setDrawer(null);
          }}
        />
      ) : null}
      {dialog?.kind === "stockOutForm" ? (
        <StockOutDialog
          invoice={dialog.invoice}
          onClose={closeDialog}
          onPosted={() => {
            closeDialog();
            setDrawer({ kind: "invoice", id: dialog.invoice.id });
          }}
        />
      ) : null}
      {dialog?.kind === "return" ? (
        <ReturnDialog
          documentId={dialog.documentId}
          onClose={closeDialog}
          onPosted={(r) => {
            closeDialog();
            setDrawer({ kind: "return", id: r.id });
          }}
        />
      ) : null}
      {dialog?.kind === "exchange" ? (
        <ExchangeDialog
          documentId={dialog.documentId}
          onClose={closeDialog}
          onPosted={(x) => {
            closeDialog();
            setDrawer({ kind: "exchange", id: x.id });
          }}
        />
      ) : null}
      {dialog?.kind === "refund" ? <RefundDialog salesReturn={dialog.salesReturn} onClose={closeDialog} /> : null}
    </SalesOverlayContext.Provider>
  );
}
