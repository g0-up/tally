import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { Button } from "../../../shared/ui/button";
import { OUTCOME_UNKNOWN_MESSAGE } from "../../../shared/hooks/create-once";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import type { SalesLineInput } from "../components/line-items/draft";
import { invoiceSchema, quoteSchema } from "../schemas";
import { useSaveInvoice, type InvoiceSaveVars } from "./invoice-mutations";
import { useQuoteTransition, useSaveQuote, type QuoteSaveVars } from "./quote-mutations";

const TOTALS = { goods_amount: "100000", discount_amount: "0", tax_amount: "0", total_amount: "100000" };
const line = (qty: string): SalesLineInput => ({ sku_id: "sku-1", unit_id: "unit-hop", qty });

const invoice = (version: number) => ({
  id: "inv-9",
  doc_no: "SO-0009",
  customer_party_id: "cus-1",
  payer_party_id: "cus-1",
  invoice_date: "2026-10-12",
  due_date: "2026-10-12",
  status: "draft",
  delivered_amount: "0",
  quote_id: null,
  copied_from_id: null,
  note: null,
  current_version: version,
  created_at: "2026-10-18T08:00:00Z",
  version,
  ...TOTALS,
});

const quote = (version: number, status = "draft") => ({
  id: "q-9",
  doc_no: "BG-0009",
  customer_party_id: "cus-1",
  quote_date: "2026-10-18",
  valid_until: "2026-10-25",
  status,
  note: null,
  converted_invoice_id: null,
  copied_from_id: null,
  revision_no: 1,
  version,
  ...TOTALS,
});

function SaveButton({ run, pending }: { run: () => void; pending: boolean }) {
  return (
    <Button variant="primary" pending={pending} onClick={run}>
      Lưu
    </Button>
  );
}

function InvoiceForm({ vars, after }: { vars: () => InvoiceSaveVars; after: () => void }) {
  const save = useSaveInvoice(after);
  return <SaveButton pending={save.pending} run={() => void save.run(vars())} />;
}

function QuoteForm({ vars, after }: { vars: () => QuoteSaveVars; after: () => void }) {
  const save = useSaveQuote(after);
  return <SaveButton pending={save.pending} run={() => void save.run(vars())} />;
}

/** "Gửi khách" on a draft quote already saved. */
function SendQuote({ after }: { after: () => void }) {
  const send = useQuoteTransition(after);
  return <SaveButton pending={send.pending} run={() => void send.run({ quote: quoteSchema.parse(quote(1)), action: "send" })} />;
}

/** "Gửi khách" on two rows of the quotes table, which share one hook. */
function SendQuoteRows({ after }: { after: () => void }) {
  const send = useQuoteTransition(after);
  const row = (id: string, docNo: string) => (
    <Button pending={send.pending} onClick={() => void send.run({ quote: quoteSchema.parse({ ...quote(1), id, doc_no: docNo }), action: "send" })}>
      Gửi {docNo}
    </Button>
  );
  return (
    <>
      {row("q-9", "BG-0009")}
      {row("q-10", "BG-0010")}
    </>
  );
}

const invoiceVars = (qty: string): InvoiceSaveVars => ({
  invoice: null,
  fields: { customerId: "cus-1", termsDays: 0, note: "", lines: [line(qty)], paidNow: "0", paymentMethodId: null },
  confirm: false,
  override: false,
});

const quoteVars = (qty: string): QuoteSaveVars => ({
  mode: "new",
  quote: null,
  fields: { customerId: "cus-1", validUntil: "2026-10-25", note: "", lines: [line(qty)] },
});

type Sent = { method: string; path: string; key: string | null; ifMatch: string | null; body: unknown };

/** Records every write; the first create loses its answer. */
function serve(create: string, created: Record<string, unknown>, more: Record<string, (s: Sent) => Response>) {
  const sent: Sent[] = [];
  const record = async (request: Request) => {
    const text = await request.text();
    const s: Sent = {
      method: request.method,
      path: new URL(request.url).pathname.replace(/^.*\/v1/, ""),
      key: request.headers.get("Idempotency-Key"),
      ifMatch: request.headers.get("If-Match"),
      body: text ? (JSON.parse(text) as unknown) : null,
    };
    sent.push(s);
    return s;
  };
  server.use(
    http.post(apiUrl(create), async ({ request }) => {
      await record(request);
      const creates = sent.filter((s) => s.path === create).length;
      return creates === 1 ? HttpResponse.error() : HttpResponse.json(created, { status: 201 });
    }),
    http.all(new RegExp(`${create}/.+`), async ({ request }) => {
      const s = await record(request);
      const answer = more[`${s.method} ${s.path}`];
      return answer ? answer(s) : HttpResponse.json({ code: "not_found" }, { status: 404 });
    }),
  );
  return sent;
}

/** Answers each call of one route with the next answer, the last one from then on. */
const inTurn = (...answers: Array<() => Response>) => {
  let calls = 0;
  return () => (answers[Math.min(calls++, answers.length - 1)] as () => Response)();
};

async function saveTwice(change: () => void) {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Lưu" }));
  expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();
  change();
  await user.click(await screen.findByRole("button", { name: "Lưu" }));
}

describe("creating a sales document", () => {
  it("resends an invoice create that got no answer under the same key and body", async () => {
    const sent = serve("/invoices", invoice(1), {});
    const after = vi.fn();
    renderWithProviders(<InvoiceForm vars={() => invoiceVars("2")} after={after} />);
    await saveTwice(() => {});
    await waitFor(() => expect(after).toHaveBeenCalled());

    expect(sent).toHaveLength(2);
    expect(sent[0]?.key).toBeTruthy();
    expect(sent[1]).toEqual(sent[0]);
  });

  it("updates the invoice an unanswered attempt created with the lines edited since", async () => {
    const sent = serve("/invoices", invoice(1), {
      "PUT /invoices/inv-9/lines": () => HttpResponse.json(invoice(3)),
    });
    const after = vi.fn();
    let qty = "2";
    renderWithProviders(<InvoiceForm vars={() => invoiceVars(qty)} after={after} />);
    await saveTwice(() => (qty = "5"));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ invoice: expect.objectContaining({ version: 3 }) })));

    // The header the draft was created with is the form's: only the lines go out.
    const [first, retry, put, ...rest] = sent;
    expect(retry).toEqual(first);
    expect(put).toMatchObject({ method: "PUT", path: "/invoices/inv-9/lines", ifMatch: '"1"', key: null, body: { lines: [line("5")] } });
    expect(rest).toEqual([]);
  });

  it("reads the invoice again when a lines update of an edit got no answer, and resends only the lines", async () => {
    const sent = serve("/invoices", invoice(1), {
      "PATCH /invoices/inv-9": inTurn(
        () => HttpResponse.json(invoice(2)),
        () => apiError(412, "precondition_failed"),
      ),
      // The lost update was stored: the invoice is at version 3 now.
      "PUT /invoices/inv-9/lines": inTurn(
        () => HttpResponse.error(),
        () => HttpResponse.json(invoice(4)),
      ),
      "GET /invoices/inv-9": () => HttpResponse.json(invoice(3)),
    });
    const after = vi.fn();
    const edit = { ...invoiceVars("5"), invoice: invoiceSchema.parse(invoice(1)) };
    renderWithProviders(<InvoiceForm vars={() => edit} after={after} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    expect(await screen.findByText(/hay chưa\. Bấm lại để kiểm tra và lưu tiếp\./)).toBeInTheDocument();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ invoice: expect.objectContaining({ version: 4 }) })));

    expect(sent.map((s) => `${s.method} ${s.ifMatch}`)).toEqual(['PATCH "1"', 'PUT "2"', "GET null", 'PUT "3"']);
  });

  it("reads the invoice again when a header update of an edit got no answer, instead of meeting a newer version", async () => {
    const sent = serve("/invoices", invoice(1), {
      // The lost update was stored: a second one with version 1 is refused.
      "PATCH /invoices/inv-9": inTurn(
        () => HttpResponse.error(),
        () => apiError(412, "precondition_failed"),
      ),
      "GET /invoices/inv-9": () => HttpResponse.json(invoice(2)),
      "PUT /invoices/inv-9/lines": () => HttpResponse.json(invoice(3)),
    });
    const after = vi.fn();
    const edit = { ...invoiceVars("5"), invoice: invoiceSchema.parse(invoice(1)) };
    renderWithProviders(<InvoiceForm vars={() => edit} after={after} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    expect(await screen.findByText(/hay chưa\. Bấm lại để kiểm tra và lưu tiếp\./)).toBeInTheDocument();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ invoice: expect.objectContaining({ version: 3 }) })));

    expect(sent.map((s) => `${s.method} ${s.ifMatch}`)).toEqual(['PATCH "1"', "GET null", 'PUT "2"']);
  });

  it("names the invoice it created when the edits since were not stored, and stores them on retry", async () => {
    const sent = serve("/invoices", invoice(1), {
      "PUT /invoices/inv-9/lines": inTurn(
        () => apiError(409, "insufficient_stock"),
        () => HttpResponse.json(invoice(3)),
      ),
    });
    const after = vi.fn();
    let qty = "2";
    renderWithProviders(<InvoiceForm vars={() => invoiceVars(qty)} after={after} />);
    await saveTwice(() => (qty = "5"));
    expect(await screen.findByText(/nhưng chưa lưu phần sửa sau: Không đủ tồn có thể bán\./)).toBeInTheDocument();
    expect(screen.getByText("SO-0009")).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();

    await userEvent.setup().click(screen.getByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ invoice: expect.objectContaining({ version: 3 }) })));
    expect(sent.filter((s) => s.method === "POST")).toHaveLength(2);
    expect(sent.slice(2).map((s) => `${s.method} ${s.ifMatch}`)).toEqual(['PUT "1"', 'PUT "1"']);
  });

  it("does not write the lines again once the customer paid, when a refused confirmation is retried", async () => {
    const sent = serve("/invoices", invoice(1), {
      "PATCH /invoices/inv-9": () => HttpResponse.json(invoice(2)),
      // With a payment recorded the lines are locked.
      "PUT /invoices/inv-9/lines": inTurn(
        () => HttpResponse.json(invoice(3)),
        () => apiError(409, "invoice_has_payments"),
      ),
      "PUT /invoices/inv-9/paid-amount": () => HttpResponse.json(invoice(4)),
      "POST /invoices/inv-9:confirm": inTurn(
        () => apiError(409, "insufficient_stock"),
        () => HttpResponse.json({ ...invoice(5), status: "confirmed" }),
      ),
    });
    const after = vi.fn();
    const edit: InvoiceSaveVars = { ...invoiceVars("5"), invoice: invoiceSchema.parse(invoice(1)), confirm: true, fields: { ...invoiceVars("5").fields, paidNow: "50000" } };
    renderWithProviders(<InvoiceForm vars={() => edit} after={after} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    expect(await screen.findByText(/Không đủ tồn có thể bán/)).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ confirmed: true })));

    const paid = sent.filter((s) => s.path.endsWith("/paid-amount"));
    expect(sent.map((s) => `${s.method} ${s.path}`)).toEqual([
      "PATCH /invoices/inv-9",
      "PUT /invoices/inv-9/lines",
      "PUT /invoices/inv-9/paid-amount",
      "POST /invoices/inv-9:confirm",
      "PUT /invoices/inv-9/paid-amount",
      "POST /invoices/inv-9:confirm",
    ]);
    expect(paid[1]?.key).toBe(paid[0]?.key);
  });

  it("resends alone a confirmation that got no answer, under the same key, and says the edits since were not stored", async () => {
    const sent = serve("/invoices", invoice(1), {
      "PATCH /invoices/inv-9": () => HttpResponse.json(invoice(2)),
      "PUT /invoices/inv-9/lines": () => HttpResponse.json(invoice(3)),
      // The lost confirmation was stored; its replay answers what it did.
      "POST /invoices/inv-9:confirm": inTurn(
        () => HttpResponse.error(),
        () => HttpResponse.json({ ...invoice(4), status: "confirmed" }),
      ),
    });
    const after = vi.fn();
    let qty = "5";
    const vars = (): InvoiceSaveVars => ({ ...invoiceVars(qty), invoice: invoiceSchema.parse(invoice(1)), confirm: true });
    renderWithProviders(<InvoiceForm vars={vars} after={after} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    expect(await screen.findByText(/nhưng chưa rõ đã xác nhận hay chưa\. Bấm lại để kiểm tra; không xác nhận lần nữa\./)).toBeInTheDocument();
    qty = "7";
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ confirmed: true })));
    expect(await screen.findByText(/Phần sửa sau lần bấm trước chưa được lưu\./)).toBeInTheDocument();

    const confirms = sent.filter((s) => s.path.endsWith(":confirm"));
    expect(sent.map((s) => s.method)).toEqual(["PATCH", "PUT", "POST", "POST"]);
    expect(confirms[1]).toEqual(confirms[0]);
    expect(confirms[0]).toMatchObject({ ifMatch: '"3"' });
  });

  it("resends a quote create that got no answer under the same key, then sends it", async () => {
    const sent = serve("/quotes", quote(1), { "POST /quotes/q-9:send": () => HttpResponse.json(quote(2, "sent")) });
    const after = vi.fn();
    renderWithProviders(<QuoteForm vars={() => quoteVars("2")} after={after} />);
    await saveTwice(() => {});
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ status: "sent" })));

    const [first, retry, send] = sent;
    expect(first?.key).toBeTruthy();
    expect(retry).toEqual(first);
    expect(send).toMatchObject({ path: "/quotes/q-9:send", ifMatch: '"1"', key: `${first?.key}:send` });
  });

  it("resends a send of the new quote that got no answer under the same key", async () => {
    const sent = serve("/quotes", quote(1), { "POST /quotes/q-9:send": inTurn(() => HttpResponse.error(), () => HttpResponse.json(quote(2, "sent"))) });
    const after = vi.fn();
    renderWithProviders(<QuoteForm vars={() => quoteVars("2")} after={after} />);
    await saveTwice(() => {});
    expect(await screen.findByText(/nhưng chưa gửi khách/)).toBeInTheDocument();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ status: "sent" })));

    const sends = sent.filter((s) => s.path === "/quotes/q-9:send");
    expect(sends).toHaveLength(2);
    expect(sends[0]?.key).toMatch(/.+:send$/);
    expect(sends[1]).toEqual(sends[0]);
  });

  it("resends “Gửi khách” that got no answer under the same key", async () => {
    const sends: Sent[] = [];
    server.use(
      http.post(apiUrl("/quotes/q-9:send"), ({ request }) => {
        sends.push({ method: request.method, path: "/quotes/q-9:send", key: request.headers.get("Idempotency-Key"), ifMatch: request.headers.get("If-Match"), body: null });
        return sends.length === 1 ? HttpResponse.error() : HttpResponse.json(quote(2, "sent"));
      }),
    );
    const after = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<SendQuote after={after} />);
    const button = await screen.findByRole("button", { name: "Lưu" });
    await user.click(button);
    await waitFor(() => expect(sends).toHaveLength(1));
    await waitFor(() => expect(button).toBeEnabled());
    await user.click(button);

    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ status: "sent" })));
    expect(sends).toHaveLength(2);
    expect(sends[0]?.key).toMatch(/.+/);
    expect(sends[1]).toEqual(sends[0]);
  });

  it("sends another quote while one send has no answer, under a key of that quote", async () => {
    // The server binds a key to the path it first stored it with and refuses it on another one.
    const bound = new Map<string, string>();
    const sends: { path: string; key: string | null }[] = [];
    server.use(
      http.post(/\/quotes\/(q-9|q-10):send$/, ({ request }) => {
        const path = new URL(request.url).pathname.replace(/^.*\/v1/, "");
        const key = request.headers.get("Idempotency-Key");
        sends.push({ path, key });
        if (key === null) return apiError(428, "idempotency_key_required");
        const first = bound.get(key);
        if (first !== undefined && first !== path) return apiError(409, "idempotency_key_reused");
        bound.set(key, path);
        return path === "/quotes/q-9:send" ? HttpResponse.error() : HttpResponse.json({ ...quote(2, "sent"), id: "q-10", doc_no: "BG-0010" });
      }),
    );
    const after = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<SendQuoteRows after={after} />);
    const first = await screen.findByRole("button", { name: "Gửi BG-0009" });
    await user.click(first);
    await waitFor(() => expect(sends).toHaveLength(1));
    await waitFor(() => expect(first).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Gửi BG-0010" }));

    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ id: "q-10", status: "sent" })));
    expect(sends.map((s) => s.path)).toEqual(["/quotes/q-9:send", "/quotes/q-10:send"]);
    expect(sends[1]?.key).not.toBe(sends[0]?.key);
  });

  it("puts the lines edited since into the quote an unanswered attempt created before sending it", async () => {
    const sent = serve("/quotes", quote(1), {
      "PATCH /quotes/q-9": () => HttpResponse.json(quote(2)),
      "POST /quotes/q-9:send": () => HttpResponse.json(quote(3, "sent")),
    });
    const after = vi.fn();
    let qty = "2";
    renderWithProviders(<QuoteForm vars={() => quoteVars(qty)} after={after} />);
    await saveTwice(() => (qty = "5"));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ version: 3 })));

    const [first, retry, patch, send] = sent;
    expect(retry).toEqual(first);
    expect(patch).toMatchObject({ method: "PATCH", ifMatch: '"1"', body: { customer_party_id: "cus-1", valid_until: "2026-10-25", draft: { lines: [line("5")] } } });
    expect(send).toMatchObject({ path: "/quotes/q-9:send", ifMatch: '"2"' });
  });

  it("reads the quote again when the update of the lines edited since got no answer, instead of meeting a newer version", async () => {
    const sent = serve("/quotes", quote(1), {
      // The lost update was stored: a second one with version 1 is refused.
      "PATCH /quotes/q-9": (s) => {
        const patches = sent.filter((x) => x.method === "PATCH").length;
        if (patches === 1) return HttpResponse.error();
        return s.ifMatch === '"1"' ? apiError(412, "precondition_failed") : HttpResponse.json(quote(3));
      },
      "GET /quotes/q-9": () => HttpResponse.json(quote(2)),
      "POST /quotes/q-9:send": () => HttpResponse.json(quote(4, "sent")),
    });
    const after = vi.fn();
    let qty = "2";
    renderWithProviders(<QuoteForm vars={() => quoteVars(qty)} after={after} />);
    await saveTwice(() => (qty = "5"));
    expect(await screen.findByText(/nhưng chưa rõ đã lưu phần sửa sau hay chưa\./)).toBeInTheDocument();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ status: "sent" })));

    expect(sent.slice(2).map((s) => `${s.method} ${s.path} ${s.ifMatch}`)).toEqual(['PATCH /quotes/q-9 "1"', "GET /quotes/q-9 null", 'PATCH /quotes/q-9 "2"', 'POST /quotes/q-9:send "3"']);
  });

  it("names the quote it created when the edits since were not stored, and neither creates nor sends it", async () => {
    const sent = serve("/quotes", quote(1), { "PATCH /quotes/q-9": () => apiError(409, "insufficient_stock") });
    const after = vi.fn();
    let qty = "2";
    renderWithProviders(<QuoteForm vars={() => quoteVars(qty)} after={after} />);
    await saveTwice(() => (qty = "5"));
    expect(await screen.findByText(/nhưng chưa lưu phần sửa sau: Không đủ tồn có thể bán\./)).toBeInTheDocument();
    expect(screen.getByText("BG-0009")).toBeInTheDocument();
    expect(sent.map((s) => `${s.method} ${s.path}`)).toEqual(["POST /quotes", "POST /quotes", "PATCH /quotes/q-9"]);
    expect(after).not.toHaveBeenCalled();
  });
});
