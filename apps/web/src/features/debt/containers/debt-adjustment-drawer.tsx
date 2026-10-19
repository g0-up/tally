import type { ReactNode } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { fd, money, vnd } from "../../../shared/lib/format";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary, type ImpactItem } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { ADJUSTMENT_KIND_LABELS, SIDE } from "../api/adapters";
import { useAdjustmentByNumber, useOffsetByNumber } from "../api/queries";
import type { DebtAdjustmentDetail, DebtOffsetDetail, DocumentSettlement } from "../schemas";

export interface DebtAdjustmentDrawerProps {
  /** A DN (debt adjustment) or BT (offset) number. */
  docNo: string;
  onClose: () => void;
}

const settlementColumns: Column<DocumentSettlement>[] = [
  { key: "doc", header: "Chứng từ", cell: (s) => <DocNo value={s.obligation_doc_no} /> },
  { key: "date", header: "Ngày", cell: (s) => fd(s.business_date) },
  { key: "amount", header: "Số tiền", numeric: true, cell: (s) => money(s.amount) },
];

interface Shown {
  sub: string;
  impact: ImpactItem[];
  settlements: readonly DocumentSettlement[];
  note: string | null;
}

function adjustmentShown(a: DebtAdjustmentDetail): Shown {
  const doc = a.target_doc_no ?? a.obligation?.source_doc_no ?? null;
  return {
    sub: `${a.party_name} · ${fd(a.business_date)}`,
    impact: [
      { label: "Số tiền", value: vnd(a.amount) },
      { label: "Loại", value: ADJUSTMENT_KIND_LABELS[a.kind] },
      { label: "Sổ", value: SIDE[a.direction].ledger },
      { label: "Lý do", value: a.reason_name },
      ...(doc !== null ? [{ label: "Chứng từ", value: <DocNo value={doc} /> }] : []),
    ],
    settlements: a.settlements,
    note: a.note,
  };
}

function offsetShown(o: DebtOffsetDetail): Shown {
  return {
    sub: `${o.party_name} · ${fd(o.business_date)}`,
    impact: [
      { label: "Số tiền bù trừ", value: vnd(o.amount) },
      { label: "Phải thu", value: <DocNo value={o.receivable_doc_no} /> },
      { label: "Phải trả", value: <DocNo value={o.payable_doc_no} /> },
    ],
    settlements: o.settlements,
    note: o.note,
  };
}

/** A posted DN or BT by number: what it changed and the documents it settled. */
export function DebtAdjustmentDrawer({ docNo, onClose }: DebtAdjustmentDrawerProps) {
  const offset = docNo.startsWith("BT");
  const adjustment = useAdjustmentByNumber(offset ? null : docNo);
  const netting = useOffsetByNumber(offset ? docNo : null);
  const query = offset ? netting : adjustment;
  const title = `${offset ? "Biên bản bù trừ" : "Điều chỉnh công nợ"} ${docNo}`;

  const shown = offset ? (netting.data ? offsetShown(netting.data) : undefined) : adjustment.data ? adjustmentShown(adjustment.data) : undefined;
  let body: ReactNode;
  if (query.isError) body = <DrawerError message={errorMessage(query.error)} />;
  else if (query.data === null) body = <DrawerError message={`Không tìm thấy ${docNo}.`} />;
  else if (shown === undefined) body = <ListLoading />;
  else
    body = (
      <>
        <ImpactSummary items={shown.impact} />
        <DataTable caption={`Chứng từ được giảm bởi ${docNo}`} columns={settlementColumns} rows={shown.settlements} rowKey={(s) => s.id} empty="Không giảm chứng từ nào." flat />
        {shown.note ? <p className="hint">{shown.note}</p> : null}
      </>
    );

  return (
    <DetailDrawer open onClose={onClose} title={title} {...(shown ? { sub: shown.sub } : {})} docKey={docNo}>
      {body}
    </DetailDrawer>
  );
}
