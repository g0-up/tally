import { useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { isApiError, isStale } from "../../../shared/api/api-error";
import { PATHS, exceptionConfirmationSchema } from "../../../shared/api/endpoints";
import type { HttpClient } from "../../../shared/api/http";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { createOnce, newCreateState, outcomeUnknown, requestJson, useUnsettled, type CreateState } from "../../../shared/hooks/create-once";
import { PartialCommitError, useCommit } from "../../../shared/hooks/use-commit";
import { EXCEPTION_SUB, exceptionTitle, useConfirmException, type ConfirmException, type ExceptionPrompt } from "../../../shared/hooks/use-confirm-exception";
import { addDays } from "../../../shared/lib/dates";
import { payloadHash } from "../../../shared/lib/canonical-hash";
import { dec, sumDec, toDecimalString } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import type { SalesLineInput } from "../components/line-items/draft";
import { invoiceCheckSchema, invoiceSchema, paymentResultSchema, stockOutSchema, type Invoice, type PaymentResult, type StockOut } from "../schemas";
import { POSTING_KEYS, editsNotSaved, etagOf, translateSalesError, translated, withDoc, withExceptionIds, withExceptions } from "./mutations";
import { salesKeys } from "./queries";

// Write side of the invoices screen. Saving a draft creates it under the intent's Idempotency-Key
// or updates the header and the lines that differ from what the intent stored, with If-Match;
// what the customer paid is recorded with its own Idempotency-Key; confirming
// reserves stock and may need the owner's exceptions (credit limit, minimum price). Delivering is
// one stock-out that also posts the sales document and the receivable.

const INVOICE_KEYS = [salesKeys.invoices()];

export interface InvoiceFields {
  customerId: string;
  /** Days of credit from the invoice date; 0 = pay now. */
  termsDays: number;
  note: string;
  lines: SalesLineInput[];
  /** Amount paid with this save ("Khách đã thanh toán" / "Khách trả thêm"); "0" for none. */
  paidNow: string;
  paymentMethodId: string | null;
}

export interface InvoiceSaveVars {
  /** The draft being edited; null creates a new invoice. */
  invoice: Invoice | null;
  fields: InvoiceFields;
  /** "Lưu & xác nhận bán" (true) or "Lưu nháp" (false). */
  confirm: boolean;
  /** The owner ticked "Tôi xác nhận vẫn bán" on the form. */
  override: boolean;
}

interface InvoiceCreateBody {
  customer_party_id: string;
  payer_party_id: string;
  invoice_date: string;
  due_date: string;
  note?: string;
  lines: SalesLineInput[];
}

export interface InvoiceSaved {
  invoice: Invoice;
  confirmed: boolean;
  /** At least one owner exception went with the confirmation. */
  exceptions: boolean;
  /** Confirmed as an earlier attempt sent it; the form's edits since were not stored. */
  editsDropped: boolean;
}

/** Ignores the "nothing changed" refusal of an update that had nothing new to store. */
async function unlessUnchanged(send: () => Promise<Invoice>): Promise<Invoice | null> {
  try {
    return await send();
  } catch (e) {
    if (isApiError(e) && e.code === "nothing_changed") return null;
    throw e;
  }
}

function invoicePrompt(kind: string, invoice: Pick<Invoice, "id" | "doc_no">, version: number): ExceptionPrompt {
  return { kind, subject: { doc_type: "invoice", doc_id: invoice.id, doc_version: version }, payload: {}, title: exceptionTitle(invoice.doc_no), sub: EXCEPTION_SUB };
}

/**
 * Stores the confirmations the owner gave on the form for every exception the server will ask for.
 * Each one is bound to the invoice version and to the hash of the confirm body ({}).
 */
async function formConfirmations(client: HttpClient, invoice: Invoice, key: string): Promise<string[]> {
  const check = await client.post(`/invoices/${invoice.id}:check`, undefined, { schema: invoiceCheckSchema });
  const hash = await payloadHash({});
  const ids: string[] = [];
  for (const kind of check.exceptions_required) {
    const body = { kind, doc_type: "invoice", doc_id: invoice.id, doc_version: invoice.version, payload_hash: hash };
    const res = await client.post(PATHS.exceptionConfirmations, body, { idempotencyKey: `${key}:exception:${kind}:${invoice.version}`, schema: exceptionConfirmationSchema });
    ids.push(res.id);
  }
  return ids;
}

/**
 * `:confirm` with the confirmations already given, asking the owner for any further exception.
 * `sending` hears each body that goes out (the exception ids it carries), so a lost answer can be
 * resent as it was.
 */
async function confirmInvoice(
  client: HttpClient,
  invoice: Invoice,
  key: string,
  confirm: ConfirmException,
  given: readonly string[],
  sending: (ids: readonly string[]) => void = () => {},
): Promise<{ invoice: Invoice; exceptions: boolean }> {
  let asked = false;
  const confirmed = await withExceptions(
    (ids) => {
      sending(ids);
      return client.post(`/invoices/${invoice.id}:confirm`, withExceptionIds({}, ids), { idempotencyKey: `${key}:confirm`, ifMatch: etagOf(invoice.version), schema: invoiceSchema });
    },
    confirm,
    (kind) => {
      asked = true;
      return invoicePrompt(kind, invoice, invoice.version);
    },
    given,
  );
  return { invoice: confirmed, exceptions: asked || given.length > 0 };
}

/** The header fields an edit writes, as attempts compare them. */
const headerJson = (h: { customer_party_id: string; payer_party_id: string; due_date?: string | null; note?: string | null }) =>
  requestJson({ customer_party_id: h.customer_party_id, payer_party_id: h.payer_party_id, due_date: h.due_date ?? null, note: h.note ?? "" });

/** What one "Lưu" intent stored, so a retry writes only what is still missing. */
interface InvoiceProgress {
  key: string;
  /** Business date of a new invoice, fixed when the intent starts so every retry sends the same body. */
  date: string;
  /** The invoice the intent works on, as its last answer left it. */
  invoice: Invoice | null;
  /** The header and the lines the server holds after the intent's last answered write; null when not known. */
  header: string | null;
  lines: string | null;
  /** An edit lost its answer or met a newer version: read the invoice before writing again. */
  stale: boolean;
  /** `:confirm` went out and was not refused: the next attempt resends it alone, as it was. */
  confirming: { invoice: Invoice; ids: readonly string[] } | null;
  create: CreateState<InvoiceCreateBody, Invoice>;
}

/** soModal submit ("Lưu & xác nhận bán") and "Lưu nháp". */
export function useSaveInvoice(after: (saved: InvoiceSaved) => void) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const confirm = useConfirmException();
  const progress = useRef<InvoiceProgress | null>(null);
  const lost = useUnsettled();
  const today = useBusinessDate();

  const commit = useCommit({
    mutationFn: async (v: InvoiceSaveVars, { idempotencyKey }): Promise<InvoiceSaved> => {
      const f = v.fields;
      const note = f.note.trim();
      if (progress.current?.key !== idempotencyKey) {
        progress.current = { key: idempotencyKey, date: today, invoice: v.invoice, header: null, lines: null, stale: false, confirming: null, create: newCreateState() };
      }
      const p = progress.current;
      const headerFor = (date: string) => ({ customer_party_id: f.customerId, payer_party_id: f.customerId, due_date: addDays(date, f.termsDays), note });
      const linesJson = requestJson(f.lines);
      const refresh = () => queryClient.invalidateQueries({ queryKey: salesKeys.invoices() });

      const confirmStep = async (invoice: Invoice, given: readonly string[], editsDropped: boolean): Promise<InvoiceSaved> => {
        try {
          const done = await confirmInvoice(client, invoice, idempotencyKey, confirm, given, (ids) => {
            p.confirming = { invoice, ids };
          });
          return { invoice: done.invoice, confirmed: true, exceptions: done.exceptions, editsDropped };
        } catch (e) {
          // The draft is stored even though it was not confirmed: show it in the list.
          await refresh();
          if (outcomeUnknown(e)) throw new PartialCommitError(withDoc("Đã lưu nháp ", invoice.doc_no, " nhưng chưa rõ đã xác nhận hay chưa. Bấm lại để kiểm tra; không xác nhận lần nữa."));
          p.confirming = null;
          if (isStale(e)) p.stale = true;
          throw translateSalesError(e);
        }
      };

      // A confirmation whose answer was lost is resent alone, under its key, version and exceptions:
      // the server answers what it did. Edits made since cannot go into a confirmed invoice.
      if (p.confirming !== null) {
        const sent = p.confirming.invoice;
        const editsDropped = p.header !== headerJson(headerFor(sent.invoice_date)) || p.lines !== linesJson;
        return confirmStep(sent, p.confirming.ids, editsDropped);
      }

      /** Writes one part of the draft and records what the server holds after it. */
      const edit = async (part: "header" | "lines", value: string, send: (invoice: Invoice) => Promise<Invoice>) => {
        const at = p.invoice as Invoice;
        const before = p[part];
        p[part] = null;
        try {
          p.invoice = (await unlessUnchanged(() => send(at))) ?? at;
          p[part] = value;
        } catch (e) {
          if (isStale(e)) {
            p.stale = true;
            p.header = p.lines = null;
          } else if (outcomeUnknown(e)) p.stale = true;
          else p[part] = before;
          throw e;
        }
      };

      let paying = false;
      try {
        if (p.invoice === null) {
          const date = p.date;
          const body: InvoiceCreateBody = { customer_party_id: f.customerId, payer_party_id: f.customerId, invoice_date: date, due_date: addDays(date, f.termsDays), ...(note ? { note } : {}), lines: f.lines };
          const made = await lost.watch(p.create, () => createOnce(p.create, body, { send: (b) => client.post("/invoices", b, { idempotencyKey, schema: invoiceSchema }) }));
          // The draft holds the body that created it, maybe an earlier attempt's: the form's edits
          // since go out as an update below.
          const sent = p.create.sent ?? body;
          p.invoice = made.result;
          p.header = headerJson(sent);
          p.lines = requestJson(sent.lines);
        }
        if (p.stale) {
          // A lost answer may have stored the edit, and moved the version: start from what is stored.
          const id = p.invoice.id;
          p.invoice = await client.get(`/invoices/${id}`, { schema: invoiceSchema });
          p.header = headerJson(p.invoice);
          p.stale = false;
        }
        const header = headerFor(p.invoice.invoice_date);
        if (p.header !== headerJson(header)) {
          await edit("header", headerJson(header), (at) => client.patch(`/invoices/${at.id}`, header, { ifMatch: etagOf(at.version), schema: invoiceSchema }));
        }
        if (p.lines !== linesJson) {
          await edit("lines", linesJson, (at) => client.put(`/invoices/${at.id}/lines`, { lines: f.lines }, { ifMatch: etagOf(at.version), schema: invoiceSchema }));
        }

        if (dec(f.paidNow).gt(0)) {
          paying = true;
          const invoice = p.invoice;
          const before = v.invoice?.payment?.paid_amount ?? "0";
          const amount = toDecimalString(sumDec([before, f.paidNow]));
          // The key carries the amount: a retry with the same amount replays the payment, one with a
          // corrected amount is a new request rather than a reused key with another body.
          const paid = await client.put(
            `/invoices/${invoice.id}/paid-amount`,
            {
              amount,
              ...(f.paymentMethodId ? { payment_method_id: f.paymentMethodId } : {}),
              note: `Khách thanh toán ${invoice.doc_no}`,
            },
            { idempotencyKey: `${idempotencyKey}:paid:${amount}`, schema: invoiceSchema },
          );
          // A replayed payment answers with the version it left; an edit since then is newer.
          if (paid.version >= invoice.version) p.invoice = paid;
        }
      } catch (e) {
        const created = v.invoice === null ? p.create.result : null;
        if (paying) throw translateSalesError(e);
        if (created === null) {
          if (p.invoice !== null && outcomeUnknown(e)) throw new PartialCommitError(withDoc("Chưa rõ đã lưu ", p.invoice.doc_no, " hay chưa. Bấm lại để kiểm tra và lưu tiếp."));
          throw translateSalesError(e);
        }
        // This intent created the draft but did not store all of the form: say which one it is.
        await refresh();
        throw editsNotSaved(created.doc_no, e);
      }

      const invoice = p.invoice;
      if (!v.confirm) return { invoice, confirmed: false, exceptions: false, editsDropped: false };
      let given: string[];
      try {
        given = v.override ? await formConfirmations(client, invoice, idempotencyKey) : [];
      } catch (e) {
        await refresh();
        throw translateSalesError(e);
      }
      return confirmStep(invoice, given, false);
    },
    success: (s) =>
      s.confirmed
        ? withDoc("Đã xác nhận ", s.invoice.doc_no, ` và giữ hàng.${s.exceptions ? " Ngoại lệ đã ghi nhật ký." : ""}${s.editsDropped ? " Phần sửa sau lần bấm trước chưa được lưu." : ""}`)
        : withDoc("Đã lưu nháp ", s.invoice.doc_no, ". Nháp chưa giữ hàng."),
    invalidate: (s) => (s.confirmed ? POSTING_KEYS : INVOICE_KEYS),
    after: (s) => {
      progress.current = null;
      after(s);
    },
  });
  return { ...commit, unsettled: lost.unsettled };
}

/** Drawer "Xác nhận bán": reserves stock; exceptions are asked one by one. */
export function useConfirmInvoice() {
  const client = useApiClient();
  const confirm = useConfirmException();
  return useCommit({
    mutationFn: (invoice: Invoice, { idempotencyKey }) => translated(() => confirmInvoice(client, invoice, idempotencyKey, confirm, [])),
    success: (r) => (r.exceptions ? "Đã xác nhận và giữ hàng. Ngoại lệ đã ghi nhật ký." : "Đã xác nhận và giữ hàng."),
    invalidate: POSTING_KEYS,
  });
}

function reservedTotal(invoice: Invoice): string {
  return toDecimalString(sumDec((invoice.lines ?? []).map((l) => l.reserved_base_qty)));
}

/** "Giữ thêm hàng": reserves what became sellable since the confirmation; the drawer stays open. */
export function useReserveMore() {
  const client = useApiClient();
  return useCommit({
    mutationFn: async (invoice: Invoice, { idempotencyKey }) => {
      try {
        const updated = await client.post(`/invoices/${invoice.id}:reserve-more`, undefined, { idempotencyKey, ifMatch: etagOf(invoice.version), schema: invoiceSchema });
        return { invoice: updated, added: toDecimalString(dec(reservedTotal(updated)).minus(reservedTotal(invoice))) };
      } catch (e) {
        // Nothing more to reserve is an answer, not a failure.
        if (isApiError(e) && e.code === "nothing_to_reserve") return { invoice, added: "0" };
        throw translateSalesError(e);
      }
    },
    success: (r) => (dec(r.added).gt(0) ? `Giữ thêm ${num(r.added)} cho ${r.invoice.doc_no}.` : "Không có thêm tồn có thể bán để giữ."),
    invalidate: [salesKeys.invoices(), ["inventory"]],
  });
}

/** soCancel "Hủy phần còn lại": releases what is still reserved; delivered goods stay posted. */
export function useCancelRemainder(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (invoice: Invoice, { idempotencyKey }) =>
      translated(() => client.post(`/invoices/${invoice.id}:cancel-remainder`, undefined, { idempotencyKey, ifMatch: etagOf(invoice.version), schema: invoiceSchema })),
    success: () => "Đã hủy phần chưa giao.",
    invalidate: [salesKeys.invoices(), ["inventory"], ["dashboard"]],
    after: () => after(),
  });
}

/** soDel "Xóa hóa đơn": a draft without any payment. */
export function useDeleteInvoice(after: () => void) {
  const client = useApiClient();
  return useCommit<unknown, Invoice>({
    mutationFn: (invoice) => translated(() => client.delete<unknown>(`/invoices/${invoice.id}`, { ifMatch: etagOf(invoice.version) })),
    success: () => "Đã xóa hóa đơn nháp.",
    invalidate: INVOICE_KEYS,
    after,
  });
}

/** "Sao chép": a new draft invoice with the same customer and lines. */
export function useCopyInvoice(after: (copy: Invoice) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (invoice: Invoice) => translated(() => client.post(`/invoices/${invoice.id}:copy`, undefined, { schema: invoiceSchema })),
    success: (copy) => withDoc("Đã sao chép thành ", copy.doc_no, " (nháp)."),
    invalidate: INVOICE_KEYS,
    after: (copy) => after(copy),
  });
}

export interface DepositVars {
  invoice: Invoice;
  amount: string;
  paymentMethodId: string;
  note: string;
}

/**
 * soDeposit "Ghi phiếu thu": money the customer pays against the invoice. It is earmarked to the
 * invoice, so it settles what is already delivered and the rest waits for the next stock-out.
 */
export function useInvoiceDeposit(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: DepositVars, { idempotencyKey }): Promise<PaymentResult> =>
      translated(() =>
        client.post(
          "/payments",
          {
            direction: "in",
            party_id: v.invoice.payer_party_id,
            purpose: "deposit",
            payment_method_id: v.paymentMethodId,
            amount: v.amount,
            source_type: "invoice",
            source_id: v.invoice.id,
            earmarked_invoice_id: v.invoice.id,
            ...(v.note.trim() ? { note: v.note.trim() } : {}),
          },
          { idempotencyKey, schema: paymentResultSchema },
        ),
      ),
    success: (p) => withDoc("Đã ghi ", p.doc_no, "."),
    invalidate: [salesKeys.invoices(), ["debt"], ["cash"], ["dashboard"]],
    after: () => after(),
  });
}

export interface StockOutVars {
  invoice: Invoice;
  lines: { invoice_line_id: string; base_qty: string }[];
}

/** newDLV "Ghi sổ xuất kho": takes stock, posts the sales document and the receivable. */
export function usePostStockOut(after: (stockOut: StockOut) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: StockOutVars, { idempotencyKey }) =>
      translated(() => client.post(`/invoices/${v.invoice.id}/stock-outs`, { lines: v.lines }, { idempotencyKey, schema: stockOutSchema })),
    success: (so) =>
      so.sales_document
        ? withDoc("Đã xuất kho ", so.doc_no, `, ghi chứng từ bán ${so.sales_document.doc_no}.`)
        : withDoc("Đã xuất kho ", so.doc_no, "."),
    invalidate: POSTING_KEYS,
    after: (so) => after(so),
  });
}
