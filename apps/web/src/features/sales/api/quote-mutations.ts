import { useRef } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { isApiError } from "../../../shared/api/api-error";
import { createOnce, newCreateState, outcomeUnknown, requestJson, useUnsettled, type CreateState } from "../../../shared/hooks/create-once";
import { runFollowUps, useCommit } from "../../../shared/hooks/use-commit";
import type { SalesLineInput } from "../components/line-items/draft";
import { invoiceSchema, quoteSchema, type Invoice, type Quote } from "../schemas";
import { editsNotSaved, etagOf, translateSalesError, translated, withDoc } from "./mutations";
import { salesKeys } from "./queries";

// Write side of the quotes screen. A quote never moves stock or money: creating and sending go under
// the intent's Idempotency-Key, so a retry after a lost answer gets the first answer back; editing
// and the other status transitions are plain updates (If-Match on the version the screen showed);
// converting it creates a draft invoice that still has to be confirmed.

export interface QuoteFields {
  customerId: string;
  validUntil: string;
  note: string;
  lines: SalesLineInput[];
}

export type QuoteSaveMode = "new" | "draft" | "edit" | "revise";

export interface QuoteSaveVars {
  /** "new": create and send; "draft": create only; "edit": change a draft; "revise": new version. */
  mode: QuoteSaveMode;
  quote: Quote | null;
  fields: QuoteFields;
}

/** The fields shared by `POST /quotes`, `PATCH /quotes/{id}` and `:revise`. */
function header(f: QuoteFields) {
  const note = f.note.trim();
  return { customer_party_id: f.customerId, valid_until: f.validUntil, ...(note ? { note } : {}) };
}

type QuoteCreateBody = ReturnType<typeof header> & { lines: SalesLineInput[] };

const QUOTE_KEYS = [salesKeys.quotes()];

const draftJson = (b: QuoteCreateBody) => requestJson(b);

/** What one create intent stored, so a retry writes only what is still missing. */
interface QuoteProgress {
  key: string;
  create: CreateState<QuoteCreateBody, Quote>;
  /** Follow-ups done after the create. */
  done: Set<string>;
  /** The quote the intent made, as its last answer left it. */
  quote: Quote | null;
  /** Header and lines the server holds after the intent's last answered write; null when not known. */
  draft: string | null;
  /** An edit lost its answer or met a newer version: read the quote before writing again. */
  stale: boolean;
}

/** quoteModal submit and "Lưu nháp". */
export function useSaveQuote(after: (quote: Quote) => void) {
  const client = useApiClient();
  const created = useRef<QuoteProgress | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: (v: QuoteSaveVars, { idempotencyKey }) =>
      translated(async () => {
        const f = v.fields;
        if (v.mode === "new" || v.mode === "draft") {
          if (created.current?.key !== idempotencyKey) created.current = { key: idempotencyKey, create: newCreateState(), done: new Set(), quote: null, draft: null, stale: false };
          const c = created.current;
          const body: QuoteCreateBody = { ...header(f), lines: f.lines };
          if (c.quote === null) {
            const made = await lost.watch(c.create, () => createOnce(c.create, body, { send: (b) => client.post("/quotes", b, { idempotencyKey, schema: quoteSchema }) }));
            c.quote = made.result;
            c.draft = draftJson(c.create.sent ?? body);
          }
          // Created from an earlier attempt's body: the form's edits since go into the draft.
          try {
            if (c.stale) {
              c.quote = await client.get(`/quotes/${c.quote.id}`, { schema: quoteSchema });
              c.stale = false;
            }
            const wanted = draftJson(body);
            if (c.draft !== wanted) {
              const at = c.quote;
              c.draft = null;
              try {
                c.quote = await client.patch(`/quotes/${at.id}`, { ...header(f), draft: { lines: f.lines } }, { ifMatch: etagOf(at.version), schema: quoteSchema });
                c.draft = wanted;
              } catch (e) {
                if ((isApiError(e) && e.status === 412) || outcomeUnknown(e)) c.stale = true;
                throw e;
              }
            }
          } catch (e) {
            throw editsNotSaved(c.quote.doc_no, e);
          }
          const stored = { quote: c.quote };
          if (v.mode === "draft") return stored.quote;
          const send = async () => {
            // A key of its own: the server binds a key to the path, and the create used the intent's.
            stored.quote = await client.post(`/quotes/${stored.quote.id}:send`, undefined, {
              idempotencyKey: `${idempotencyKey}:send`,
              ifMatch: etagOf(stored.quote.version),
              schema: quoteSchema,
            });
            c.quote = stored.quote;
          };
          await runFollowUps([{ id: "send", label: "gửi khách", run: send }], c.done, (missing, reason) => withDoc("Đã lưu ", stored.quote.doc_no, ` nhưng chưa ${missing}: ${reason} Bấm lại để gửi.`), translateSalesError);
          return stored.quote;
        }
        if (v.quote === null) throw new Error("Không tìm thấy báo giá để lưu.");
        const body = { ...header(f), draft: { lines: f.lines } };
        const path = v.mode === "edit" ? `/quotes/${v.quote.id}` : `/quotes/${v.quote.id}:revise`;
        const opts = { ifMatch: etagOf(v.quote.version), schema: quoteSchema };
        return v.mode === "edit" ? client.patch(path, body, opts) : client.post(path, body, opts);
      }),
    success: (q, v) => withDoc(v.mode === "draft" ? "Đã lưu nháp " : "Đã lưu ", q.doc_no, "."),
    invalidate: QUOTE_KEYS,
    after: (q) => {
      created.current = null;
      after(q);
    },
  });
  return { ...commit, unsettled: lost.unsettled };
}

export type QuoteTransition = "send" | "accept" | "reject";

const TRANSITION_TOASTS: Readonly<Record<QuoteTransition, string>> = {
  send: "Đã chuyển sang “Đã gửi”.",
  accept: "Đã ghi nhận khách chấp nhận.",
  reject: "Đã ghi nhận khách từ chối.",
};

/**
 * "Gửi khách", "Khách chấp nhận", "Khách từ chối": status changes of the version shown. Sending takes
 * the intent's key, made the quote's own since one hook serves every row of the table.
 */
export function useQuoteTransition(after?: (quote: Quote) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { quote: Quote; action: QuoteTransition }, { idempotencyKey }) =>
      translated(() =>
        client.post(`/quotes/${v.quote.id}:${v.action}`, undefined, {
          ...(v.action === "send" ? { idempotencyKey: `${idempotencyKey}:${v.quote.id}` } : {}),
          ifMatch: etagOf(v.quote.version),
          schema: quoteSchema,
        }),
      ),
    success: (_q, v) => TRANSITION_TOASTS[v.action],
    invalidate: QUOTE_KEYS,
    ...(after ? { after: (q: Quote) => after(q) } : {}),
  });
}

/** "Xóa": only a draft quote can be deleted. */
export function useDeleteQuote(after: () => void) {
  const client = useApiClient();
  return useCommit<unknown, Quote>({
    mutationFn: (quote) => translated(() => client.delete<unknown>(`/quotes/${quote.id}`, { ifMatch: etagOf(quote.version) })),
    success: () => "Đã xóa báo giá nháp.",
    invalidate: QUOTE_KEYS,
    after,
  });
}

/** "Sao chép": a new draft quote with the same customer and lines. */
export function useCopyQuote(after?: (copy: Quote) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (quote: Quote) => translated(() => client.post(`/quotes/${quote.id}:copy`, undefined, { schema: quoteSchema })),
    success: (copy) => withDoc("Đã sao chép thành ", copy.doc_no, " (nháp)."),
    invalidate: QUOTE_KEYS,
    ...(after ? { after: (copy: Quote) => after(copy) } : {}),
  });
}

/** "Chuyển thành hóa đơn": a draft invoice from an accepted quote. */
export function useConvertQuote(after: (invoice: Invoice) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (quote: Quote) => translated(() => client.post(`/quotes/${quote.id}:convert`, undefined, { ifMatch: etagOf(quote.version), schema: invoiceSchema })),
    success: (invoice) => withDoc("Đã tạo hóa đơn ", invoice.doc_no, " (nháp) từ báo giá. Xác nhận bán để giữ hàng."),
    invalidate: [salesKeys.quotes(), salesKeys.invoices()],
    after: (invoice) => after(invoice),
  });
}
