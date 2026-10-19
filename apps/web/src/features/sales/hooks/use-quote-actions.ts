import { useState } from "react";
import { useConvertQuote, useCopyQuote, useDeleteQuote, useQuoteTransition } from "../api/quote-mutations";
import type { QuoteAction } from "../components/quote-actions";
import type { Quote } from "../schemas";
import { useSalesOverlay } from "./use-sales-overlay";

export interface QuoteActionRunner {
  /** The quote and action in flight, for the busy button. */
  pending: { id: string; action: QuoteAction } | null;
  run: (quote: Quote, action: QuoteAction) => void;
}

/** What each quote action button does, shared by the quotes table and the quote drawer. */
export function useQuoteActions(): QuoteActionRunner {
  const overlay = useSalesOverlay();
  const [pending, setPending] = useState<QuoteActionRunner["pending"]>(null);
  const transition = useQuoteTransition();
  const del = useDeleteQuote(() => overlay.closeDrawer());
  const copy = useCopyQuote();
  const convert = useConvertQuote((invoice) => overlay.openDrawer({ kind: "invoice", id: invoice.id }));

  const busy = async (quote: Quote, action: QuoteAction, send: () => Promise<unknown>) => {
    setPending({ id: quote.id, action });
    try {
      await send();
    } finally {
      setPending(null);
    }
  };

  const run = (quote: Quote, action: QuoteAction) => {
    if (pending !== null) return;
    switch (action) {
      case "edit":
      case "revise":
        overlay.openDialog({ kind: "quoteForm", mode: action, quote });
        return;
      case "reject":
        overlay.openDialog({ kind: "quoteReject", quote });
        return;
      case "send":
      case "accept":
        void busy(quote, action, () => transition.run({ quote, action }));
        return;
      case "delete":
        void busy(quote, action, () => del.run(quote));
        return;
      case "copy":
        void busy(quote, action, () => copy.run(quote));
        return;
      case "convert":
        void busy(quote, action, () => convert.run(quote));
        return;
    }
  };

  return { pending, run };
}
