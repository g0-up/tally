import { useQueries, useQuery } from "@tanstack/react-query";
import { z, type ZodType } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import type { HttpClient } from "../../../shared/api/http";
import type { ComboItem } from "../../../shared/ui/combobox";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import {
  agingSchema,
  collectionNoteSchema,
  consentsSchema,
  customerProfileSchema,
  debtAdjustmentDetailSchema,
  debtOffsetDetailSchema,
  directoryPartySchema,
  forecastSchema,
  fundSchema,
  partyLedgerSchema,
  partySummarySchema,
  paymentDetailSchema,
  paymentMethodSchema,
  paymentRowSchema,
  printStatementSchema,
  reasonSchema,
  statementRowSchema,
  statementVersionSchema,
  supplierProfileSchema,
  type DebtDirection,
  type Statement,
} from "../schemas";

// Read side of the Công nợ screens. Every key starts with "debt", the prefix the postings of other
// screens (sales, purchasing) already refresh, so balances, advances and statements stay current.
// Statement versions are the exception: a written version never changes, so its key stays outside
// that prefix and no posting reads it again. Payments, adjustments and offsets opened by number
// sit under "debt" too, so a posting that allocates or reverses one shows its new state.

export const debtKeys = {
  all: ["debt"] as const,
  parties: (direction: DebtDirection) => ["debt", "parties", "list", direction] as const,
  ledger: (partyId: string) => ["debt", "parties", partyId, "ledger"] as const,
  notes: (partyId: string) => ["debt", "parties", partyId, "notes"] as const,
  aging: (direction: DebtDirection, asOf: string) => ["debt", "aging", direction, asOf] as const,
  forecasts: ["debt", "forecast"] as const,
  forecast: (from: string, to: string) => ["debt", "forecast", from, to] as const,
  /** Every statement read: the list, one statement, its print data. */
  allStatements: ["debt", "statements"] as const,
  statements: ["debt", "statements", "list"] as const,
  statement: (id: string) => ["debt", "statements", id] as const,
  advances: ["debt", "advances"] as const,
  document: (kind: string, docNo: string) => ["debt", "documents", kind, docNo] as const,
  statementVersion: (id: string, n: number) => ["statement-version", id, n] as const,
  printStatement: (id: string, version: number | null) => ["debt", "statements", id, "print", version] as const,
  funds: ["debt", "funds"] as const,
  directory: (direction: DebtDirection) => ["debt", "directory", direction] as const,
  lookup: (...rest: readonly unknown[]) => ["debt", "lookup", ...rest] as const,
};

const LOOKUP_STALE_MS = 5 * 60_000;
/** Rows one page of the party lists holds (the API maximum). */
const PAGE = 200;
/** Statements one page shows. */
const STATEMENT_PAGE = 50;
/** Advances one page shows. */
const ADVANCE_PAGE = 50;

/** Parties with open documents or an advance on one ledger (`GET /debts/parties`). */
export function useDebtParties(direction: DebtDirection) {
  return useCursorList({ queryKey: debtKeys.parties(direction), path: "/debts/parties", item: partySummarySchema, params: { direction }, limit: PAGE });
}

/** Active customers or suppliers, balance or not; loaded only for the "Tất cả" chip. */
export function usePartyDirectory(direction: DebtDirection, enabled: boolean) {
  return useCursorList({
    queryKey: debtKeys.directory(direction),
    path: direction === "receivable" ? "/customers" : "/suppliers",
    item: directoryPartySchema,
    params: { status: "active" },
    limit: PAGE,
    enabled,
  });
}

/** Unpaid amounts by days past due, per party, at a date (`GET /debts/aging`); waits for the date. */
export function useAging(direction: DebtDirection, asOf: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.aging(direction, asOf ?? ""),
    queryFn: ({ signal }) => client.get("/debts/aging", { query: { direction, as_of: asOf }, schema: agingSchema, signal }),
    enabled: asOf !== null,
  });
}

/** One party's open documents, advances and payments waiting for verification. */
export function usePartyLedger(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.ledger(partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/debts/parties/${partyId}`, { schema: partyLedgerSchema, signal }),
    enabled: partyId !== null,
  });
}

/** Unallocated advances of both ledgers ("Ứng trước & cọc"): confirmed payments with money left to allocate. */
export function useAdvances() {
  return useCursorList({ queryKey: debtKeys.advances, path: "/payments", item: paymentRowSchema, params: { unallocated: true }, limit: ADVANCE_PAGE });
}

/** The debt documents a number opens, with their list and detail routes. */
const DOCUMENT_ROUTES = { payment: "/payments", adjustment: "/debt-adjustments", offset: "/debt-offsets" } as const;
type DocumentKind = keyof typeof DOCUMENT_ROUTES;

const idPage = listOf(z.object({ id: z.string() }));

/**
 * A debt document by its number: the list filtered by `doc_no`, then the detail with the documents
 * it settled. Null when no document carries the number (a typo or another store's link).
 */
function useDocumentByNumber<T>(kind: DocumentKind, docNo: string | null, schema: ZodType<T>) {
  const client = useApiClient();
  const path = DOCUMENT_ROUTES[kind];
  return useQuery({
    queryKey: debtKeys.document(kind, docNo ?? ""),
    queryFn: async ({ signal }): Promise<T | null> => {
      const page = await client.get(path, { query: { doc_no: docNo, limit: 1 }, schema: idPage, signal });
      const found = page.data[0];
      return found === undefined ? null : client.get(`${path}/${found.id}`, { schema, signal });
    },
    enabled: docNo !== null,
  });
}

/** A PT or PC by number (`GET /payments?doc_no=`, then `GET /payments/{id}`). */
export function usePaymentByNumber(docNo: string | null) {
  return useDocumentByNumber("payment", docNo, paymentDetailSchema);
}

/** A DN by number (`GET /debt-adjustments?doc_no=`, then `GET /debt-adjustments/{id}`). */
export function useAdjustmentByNumber(docNo: string | null) {
  return useDocumentByNumber("adjustment", docNo, debtAdjustmentDetailSchema);
}

/** A BT by number (`GET /debt-offsets?doc_no=`, then `GET /debt-offsets/{id}`). */
export function useOffsetByNumber(docNo: string | null) {
  return useDocumentByNumber("offset", docNo, debtOffsetDetailSchema);
}

/** Contact notes and promises of a party, newest first (first page). */
export function useCollectionNotes(partyId: string | null) {
  return useCursorList({
    queryKey: debtKeys.notes(partyId ?? ""),
    path: `/parties/${partyId ?? "-"}/collection-notes`,
    item: collectionNoteSchema,
    limit: 50,
    enabled: partyId !== null,
  });
}

/** Amounts falling due per day, with the overdue balances, for the schedule (`GET /debts/forecast`). */
export function useDebtForecast(from: string, to: string, enabled = true) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.forecast(from, to),
    queryFn: ({ signal }) => client.get("/debts/forecast", { query: { from, to }, schema: forecastSchema, signal }),
    enabled,
  });
}

/** Fund accounts with their balance ("Tiền đang có"). */
export function useFunds() {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.funds,
    queryFn: ({ signal }) => client.get("/funds", { schema: listOf(fundSchema), signal }),
    select: (r) => r.data.filter((f) => f.active),
  });
}

/** Active payment methods ("Vào quỹ" / "Chi từ quỹ"). */
export function usePaymentMethods() {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.lookup("payment-methods"),
    queryFn: ({ signal }) => client.get("/payment-methods", { schema: listOf(paymentMethodSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((m) => m.active),
  });
}

/** Active reasons of a kind ("write_off"). */
export function useReasons(kind: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.lookup("reasons", kind),
    queryFn: ({ signal }) => client.get("/reasons", { query: { kind, active: true }, schema: listOf(reasonSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((x) => x.active),
  });
}

/** Group, credit limit and terms of a customer, for the party drawer. */
export function useCustomerProfile(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.lookup("customer", partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/customers/${partyId}`, { schema: customerProfileSchema, signal, silent: true }),
    enabled: partyId !== null,
    retry: false,
  });
}

/** Payment terms of a supplier, for the party drawer. */
export function useSupplierProfile(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.lookup("supplier", partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/suppliers/${partyId}`, { schema: supplierProfileSchema, signal, silent: true }),
    enabled: partyId !== null,
    retry: false,
  });
}

/** Whether a customer agreed to debt reminders by message, on any channel. */
export function useReminderConsent(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.lookup("consents", partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/customers/${partyId}/consents`, { schema: consentsSchema, signal, silent: true }),
    enabled: partyId !== null,
    retry: false,
    select: (r) => r.current.some((c) => c.purpose === "debt_reminder" && c.granted),
  });
}

/** Statements with the party name and the current version's balances, newest first (`GET /statements`). */
export function useStatements() {
  return useCursorList({ queryKey: debtKeys.statements, path: "/statements", item: statementRowSchema, limit: STATEMENT_PAGE });
}

/** One statement by id, for a notification link to a statement past the first page (`GET /statements/{id}`). */
export function useStatement(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.statement(id ?? ""),
    queryFn: ({ signal }) => client.get(`/statements/${id}`, { schema: statementRowSchema, signal }),
    enabled: id !== null,
  });
}

/** Every version of a statement, oldest first, for "Lịch sử phiên bản". */
export function useStatementHistory(statement: Statement | null) {
  const client = useApiClient();
  const count = statement?.current_version ?? 0;
  return useQueries({
    queries: Array.from({ length: count }, (_, i) => i + 1).map((n) => ({
      queryKey: debtKeys.statementVersion(statement?.id ?? "", n),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/statements/${statement?.id}/versions/${n}`, { schema: statementVersionSchema, signal }),
      staleTime: Infinity,
    })),
  });
}

/** Active customers for the statement party picker. */
export const customerSource = {
  key: "debt-customer",
  search: async ({ client, query, signal }: { client: HttpClient; query: string; signal: AbortSignal }): Promise<ComboItem[]> => {
    const r = await client.get("/customers", { query: { q: query.trim(), status: "active", limit: 50 }, schema: listOf(directoryPartySchema), signal });
    return r.data.map((c) => ({ id: c.id, name: c.name, detail: [c.phone, c.customer_group_name].filter(Boolean).join(" · "), keywords: c.phone ? [c.phone] : [] }));
  },
};

/** One version of a statement with its frozen content. */
export function useStatementVersion(id: string | null, n: number | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.statementVersion(id ?? "", n ?? 0),
    queryFn: ({ signal }) => client.get(`/statements/${id}/versions/${n}`, { schema: statementVersionSchema, signal }),
    enabled: id !== null && n !== null,
    // A written version never changes.
    staleTime: Infinity,
  });
}

/** Print data of a statement version (`GET /print/statement/{id}`); without a version, the sent one. */
export function usePrintStatement(id: string, version: number | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: debtKeys.printStatement(id, version),
    queryFn: ({ signal }) => client.get(`/print/statement/${id}`, { query: { version }, schema: printStatementSchema, signal }),
  });
}
