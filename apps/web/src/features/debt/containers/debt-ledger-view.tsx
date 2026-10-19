import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { errorMessage } from "../../../shared/api/api-error";
import { dec } from "../../../shared/lib/decimal";
import { Callout } from "../../../shared/ui/callout";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { useLoadAll } from "../../inventory";
import { partySubLine } from "../api/adapters";
import { useAging, useDebtParties, usePartyDirectory } from "../api/queries";
import { DebtPartyTable, type DebtPartyRow } from "../components/debt-party-table";
import { PartyDrawer } from "../components/party-drawer";
import type { DebtDirection, LedgerDocument } from "../schemas";
import { useSearchParam } from "../use-search-param";
import { DebtAdjustmentDrawer } from "./debt-adjustment-drawer";
import { DebtNoteDialog } from "./debt-note-dialog";
import { PayDialog } from "./pay-dialog";
import { WriteOffDialog } from "./write-off-dialog";

type Scope = "open" | "all";
type Opened = { kind: "pay"; partyId: string | null } | { kind: "note"; partyId: string } | { kind: "writeOff"; doc: LedgerDocument } | null;

export interface DebtLedgerViewProps {
  direction: DebtDirection;
  /** The tenant's business date, the aging date; null until the tenant is known. */
  asOf: string | null;
  /** "Thu tiền khách" / "Trả tiền NCC" in the page header opens the payment without a party. */
  payRequested: boolean;
  onPayClosed: () => void;
}

/**
 * Body of "Khách còn nợ" and "Cửa hàng còn nợ NCC": the parties of one ledger with aging at the
 * business date, the party drawer (`?party=<id>`) and its payment, note and write-off dialogs.
 * `?doc=DN-…|BT-…` opens a posted debt adjustment or offset.
 */
export function DebtLedgerView({ direction, asOf, payRequested, onPayClosed }: DebtLedgerViewProps) {
  const navigate = useNavigate();
  const [scope, setScope] = useState<Scope>("open");
  const [opened, setOpened] = useState<Opened>(null);
  const party = useSearchParam("party");
  const doc = useSearchParam("doc");
  const parties = useDebtParties(direction);
  useLoadAll(parties);
  const aging = useAging(direction, asOf);
  const directory = usePartyDirectory(direction, scope === "all");
  useLoadAll(directory, scope === "all");

  const rows = useMemo<DebtPartyRow[]>(() => {
    const buckets = new Map((aging.data?.rows ?? []).map((r) => [r.party_id, r.buckets]));
    const known = new Map(directory.items.map((d) => [d.id, d]));
    const out: DebtPartyRow[] = parties.items.map((p) => {
      const d = known.get(p.party_id);
      return { partyId: p.party_id, name: p.name, sub: partySubLine(direction, d?.customer_group_name, d?.credit_limit, p.phone), buckets: buckets.get(p.party_id) ?? null, advance: p.advance };
    });
    if (scope === "all") {
      const listed = new Set(out.map((r) => r.partyId));
      for (const d of directory.items) {
        if (!listed.has(d.id)) out.push({ partyId: d.id, name: d.name, sub: partySubLine(direction, d.customer_group_name, d.credit_limit, d.phone), buckets: null, advance: "0" });
      }
    }
    return scope === "open" ? out.filter((r) => dec(r.buckets?.total ?? "0").gt(0) || dec(r.advance).gt(0)) : out;
  }, [aging.data, directory.items, parties.items, scope, direction]);

  const toStatement = (partyId: string) => void navigate(`/debts/statements?party=${encodeURIComponent(partyId)}`);
  const closeDialog = () => setOpened(null);

  return (
    <>
      <FilterBar>
        <ToggleGroup
          label="Phạm vi"
          options={[
            { value: "open", label: "Có số dư" },
            { value: "all", label: direction === "receivable" ? "Tất cả khách" : "Tất cả NCC" },
          ]}
          value={scope}
          onChange={setScope}
        />
      </FilterBar>
      {aging.isError ? (
        <Callout tone="bad" role="alert">
          {errorMessage(aging.error)}
        </Callout>
      ) : null}
      {parties.isLoading || aging.isLoading || directory.isLoading ? (
        <ListLoading />
      ) : (
        <DebtPartyTable direction={direction} rows={rows} onOpen={party.set} onPay={(partyId) => setOpened({ kind: "pay", partyId })} onStatement={toStatement} />
      )}
      <ListFooter list={parties} />
      {party.value !== null ? (
        <PartyDrawer
          partyId={party.value}
          direction={direction}
          onClose={party.drop}
          onPay={() => setOpened({ kind: "pay", partyId: party.value })}
          onStatement={() => toStatement(party.value ?? "")}
          onNote={() => party.value !== null && setOpened({ kind: "note", partyId: party.value })}
          onWriteOff={(doc) => setOpened({ kind: "writeOff", doc })}
        />
      ) : null}
      {doc.value !== null ? <DebtAdjustmentDrawer docNo={doc.value} onClose={doc.drop} /> : null}
      {payRequested ? <PayDialog direction={direction} partyId={null} onClose={onPayClosed} /> : null}
      {opened?.kind === "pay" ? <PayDialog direction={direction} partyId={opened.partyId} onClose={closeDialog} /> : null}
      {opened?.kind === "note" ? <DebtNoteDialog partyId={opened.partyId} onClose={closeDialog} /> : null}
      {opened?.kind === "writeOff" ? <WriteOffDialog doc={opened.doc} onClose={closeDialog} /> : null}
    </>
  );
}
