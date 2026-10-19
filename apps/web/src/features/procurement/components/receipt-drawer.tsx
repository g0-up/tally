import { useRef } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { fd, money, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { LinkButton } from "../../../shared/ui/link-button";
import { LIST_LOADING_TEXT } from "../../../shared/ui/list-footer";
import { useColorDirectory } from "../../catalog";
import { ATTACHMENT_TYPES, useAttachReceiptFile, useOpenAttachment } from "../api/attachments";
import { useReceipt } from "../api/queries";
import type { Attachment, Receipt, ReceiptLine, ReceiptPayment } from "../schemas";

export interface ReceiptDrawerProps {
  id: string;
  onClose: () => void;
  onReturn: (receipt: Receipt) => void;
}

const kb = (bytes: number) => `${num(Math.max(1, Math.round(bytes / 1024)))} KB`;

/** Prototype `openRCV`: the posted lines with their landed cost, what is owed and how it was settled. */
export function ReceiptDrawer({ id, onClose, onReturn }: ReceiptDrawerProps) {
  const receipt = useReceipt(id);
  const r = receipt.data;
  const colors = useColorDirectory();
  const attach = useAttachReceiptFile(id);
  const download = useOpenAttachment();
  const fileInput = useRef<HTMLInputElement | null>(null);

  if (!r) {
    return (
      <DetailDrawer open onClose={onClose} title="Phiếu nhận" docKey={id}>
        {receipt.error ? <DrawerError message={errorMessage(receipt.error)} /> : <p className="hint">{LIST_LOADING_TEXT}</p>}
      </DetailDrawer>
    );
  }

  const lineColumns: Column<ReceiptLine>[] = [
    {
      key: "name",
      header: "Tên sản phẩm",
      cell: (l) => (
        <>
          {l.name}
          {l.color_id ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(l.color_id)} />
            </>
          ) : null}
        </>
      ),
    },
    { key: "unit", header: "Đơn vị tính", cell: (l) => l.unit_name },
    { key: "qty", header: "Số lượng", numeric: true, cell: (l) => num(l.qty) },
    { key: "spec", header: "Quy cách", cell: (l) => l.spec_text || "—" },
    { key: "price", header: "Đơn giá", numeric: true, cell: (l) => money(l.unit_price) },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (l) => money(l.line_amount) },
    {
      key: "lot",
      header: "Lô · hạn",
      cell: (l) => (
        <>
          {l.lot_no || "—"}
          <CellSub>{fd(l.expiry_date)}</CellSub>
        </>
      ),
    },
    { key: "cost", header: "Giá vốn sau phân bổ", numeric: true, cell: (l) => (l.unit_cost ? money(l.unit_cost) : "—") },
  ];

  const settlementColumns: Column<ReceiptPayment>[] = [
    { key: "doc", header: "Nguồn", cell: (p) => <DocNo value={p.doc_no} /> },
    { key: "type", header: "Loại", cell: () => "Trả tiền NCC" },
    { key: "date", header: "Ngày", cell: (p) => fd(p.business_date) },
    { key: "amount", header: "Số tiền", numeric: true, cell: (p) => money(p.amount) },
  ];

  const attachmentColumns: Column<Attachment>[] = [
    {
      key: "name",
      header: "Tệp",
      cell: (a) => (
        <LinkButton onClick={() => void download.run(a.id)} disabled={download.pending}>
          {a.file_name}
        </LinkButton>
      ),
    },
    { key: "size", header: "Dung lượng", numeric: true, cell: (a) => kb(a.size_bytes) },
    { key: "date", header: "Ngày tải lên", cell: (a) => fd(a.created_at.slice(0, 10)) },
  ];

  const payable = r.payable;
  const settlements = r.payments ?? [];

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Phiếu nhận ${r.doc_no}`}
      sub={`${r.supplier_name ?? "—"} · ${fd(r.business_date)}${r.purchase_order_doc_no ? ` · theo ${r.purchase_order_doc_no}` : ""}`}
      docKey={r.id}
    >
      <ButtonRow>
        <Button onClick={() => onReturn(r)}>Trả NCC từ phiếu này</Button>
      </ButtonRow>
      <DataTable caption={`Dòng hàng ${r.doc_no}`} columns={lineColumns} rows={r.lines ?? []} rowKey={(l) => l.id} flat />
      <ImpactSummary
        items={[
          { label: "Nợ NCC phát sinh", value: vnd(payable?.amount ?? r.total_amount) },
          { label: "Đã trả / giảm", value: vnd(payable?.settled_amount ?? "0") },
          { label: "Còn phải trả", value: vnd(payable?.open_amount ?? "0") },
          { label: "Hạn trả", value: fd(payable?.due_date ?? r.due_date) },
        ]}
      />
      <DataTable caption="Thanh toán" columns={settlementColumns} rows={settlements} rowKey={(p) => p.id} flat empty="Chưa thanh toán." />
      <h3 className="sec-h">Ảnh chứng từ</h3>
      <DataTable caption="Ảnh chứng từ" columns={attachmentColumns} rows={r.attachments ?? []} rowKey={(a) => a.id} flat empty="Chưa có ảnh chứng từ." />
      <ButtonRow>
        <input
          ref={fileInput}
          type="file"
          accept={ATTACHMENT_TYPES.join(",")}
          hidden
          aria-label="Chọn ảnh chứng từ"
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (file) void attach.run(file);
          }}
        />
        <Button size="sm" pending={attach.pending} onClick={() => fileInput.current?.click()}>
          Đính kèm ảnh chứng từ
        </Button>
        <span className="hint">Ảnh JPG, PNG, WEBP hoặc PDF, tối đa 10 MB.</span>
      </ButtonRow>
    </DetailDrawer>
  );
}
