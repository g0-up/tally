import { money } from "../../../shared/lib/format";
import { dec, sumDec, type DecimalLike } from "../../../shared/lib/decimal";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { LinkButton } from "../../../shared/ui/link-button";
import { SIDE } from "../api/adapters";
import type { AgingBuckets, DebtDirection } from "../schemas";

/** One party on a ledger: open balance by age and the advance kept apart (never a negative debt). */
export interface DebtPartyRow {
  partyId: string;
  name: string;
  sub: string;
  buckets: AgingBuckets | null;
  advance: DecimalLike;
}

export interface DebtPartyTableProps {
  direction: DebtDirection;
  rows: readonly DebtPartyRow[];
  onOpen: (partyId: string) => void;
  onPay: (partyId: string) => void;
  /** "Bảng kê" on a receivable row. */
  onStatement: (partyId: string) => void;
}

const ZERO = "0";
/** "Quá > 60": the two oldest buckets of the API together. */
const over60 = (b: AgingBuckets | null) => (b === null ? dec(0) : dec(b.d61_90).plus(b.over_90));
const total = (rows: readonly DebtPartyRow[], pick: (r: DebtPartyRow) => DecimalLike) => money(sumDec(rows.map(pick)));

/** Prototype debtTable: "Khách còn nợ" / "Cửa hàng còn nợ NCC" with aging and advance columns. */
export function DebtPartyTable({ direction, rows, onOpen, onPay, onStatement }: DebtPartyTableProps) {
  const side = SIDE[direction];
  const columns: Column<DebtPartyRow>[] = [
    {
      key: "party",
      header: side.party,
      cell: (r) => (
        <>
          <LinkButton onClick={() => onOpen(r.partyId)}>{r.name}</LinkButton>
          {r.sub !== "" ? <CellSub>{r.sub}</CellSub> : null}
        </>
      ),
      footer: "Tổng",
    },
    { key: "open", header: side.owes, numeric: true, cell: (r) => <b>{money(r.buckets?.total ?? ZERO)}</b>, footer: total(rows, (r) => r.buckets?.total ?? ZERO) },
    { key: "current", header: "Trong hạn", numeric: true, cell: (r) => money(r.buckets?.current ?? ZERO), footer: total(rows, (r) => r.buckets?.current ?? ZERO) },
    { key: "d30", header: "Quá 1–30", numeric: true, cell: (r) => money(r.buckets?.d1_30 ?? ZERO), footer: total(rows, (r) => r.buckets?.d1_30 ?? ZERO) },
    { key: "d60", header: "Quá 31–60", numeric: true, cell: (r) => money(r.buckets?.d31_60 ?? ZERO), footer: total(rows, (r) => r.buckets?.d31_60 ?? ZERO) },
    { key: "d90", header: "Quá > 60", numeric: true, cell: (r) => money(over60(r.buckets)), footer: total(rows, (r) => over60(r.buckets)) },
    { key: "advance", header: side.advance, numeric: true, cell: (r) => money(r.advance), footer: total(rows, (r) => r.advance) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) =>
        direction === "receivable" ? (
          <>
            <Button variant="navy" size="sm" aria-label={`${side.pay} ${r.name}`} onClick={() => onPay(r.partyId)}>
              {side.pay}
            </Button>{" "}
            <Button size="sm" aria-label={`Bảng kê ${r.name}`} onClick={() => onStatement(r.partyId)}>
              Bảng kê
            </Button>
          </>
        ) : (
          <Button variant="navy" size="sm" aria-label={`${side.pay} ${r.name}`} onClick={() => onPay(r.partyId)}>
            {side.pay}
          </Button>
        ),
    },
  ];
  return <DataTable caption={side.ledger} columns={columns} rows={rows} rowKey={(r) => r.partyId} />;
}
