import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fd, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { paymentInstructionsSchema, useSaasInvoices, type PaymentInstructions, type SaasInvoice } from "../api/billing";
import { INVOICE_PURPOSE, INVOICE_STATUS } from "../labels";
import { LoadError } from "./load-error";

export const PAY_LABEL = "Thanh toán";

/** "Hóa đơn thuê bao": invoices of the store's plan; an open one shows how to pay it. */
export function InvoicesCard() {
  const client = useApiClient();
  const invoices = useSaasInvoices();
  const [instructions, setInstructions] = useState<PaymentInstructions | null>(null);
  const [paying, setPaying] = useState<string | null>(null);

  const pay = useCommit({
    mutationFn: (id: string) => client.post(`/saas-invoices/${encodeURIComponent(id)}:pay`, {}, { schema: paymentInstructionsSchema }),
    after: (r) => setInstructions(r),
  });

  const columns: ReadonlyArray<Column<SaasInvoice>> = [
    { key: "no", header: "Số", cell: (inv) => <DocNo value={inv.number} /> },
    {
      key: "period",
      header: "Kỳ",
      cell: (inv) => <CellText main={`${fd(inv.period_start)} – ${fd(inv.period_end)}`} sub={INVOICE_PURPOSE[inv.purpose]} />,
    },
    { key: "amount", header: "Số tiền", numeric: true, cell: (inv) => vnd(inv.amount) },
    {
      key: "status",
      header: "Trạng thái",
      cell: (inv) => {
        const s = INVOICE_STATUS[inv.status];
        return <StatusPill {...s} />;
      },
    },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (inv) =>
        inv.status === "open" ? (
          <Button
            variant="primary"
            size="sm"
            aria-label={`${PAY_LABEL} ${inv.number}`}
            pending={pay.pending && paying === inv.id}
            pendingLabel="Đang mở…"
            disabled={pay.pending}
            onClick={() => {
              setPaying(inv.id);
              void pay.run(inv.id);
            }}
          >
            {PAY_LABEL}
          </Button>
        ) : null,
    },
  ];

  return (
    <Card title="Hóa đơn thuê bao">
      {invoices.isPending ? (
        <ListLoading />
      ) : invoices.error ? (
        <LoadError what="hóa đơn thuê bao" error={invoices.error} />
      ) : (
        <DataTable caption="Hóa đơn thuê bao" columns={columns} rows={invoices.data} rowKey={(inv) => inv.id} flat empty="Chưa có hóa đơn thuê bao." />
      )}
      <CommitDialog
        open={instructions !== null}
        onClose={() => setInstructions(null)}
        title={instructions ? `Thanh toán hóa đơn ${instructions.invoice.number}` : PAY_LABEL}
        sub="Hóa đơn được ghi nhận đã thanh toán khi nhận được tiền; trang này cập nhật sau đó."
        cancelLabel="Đóng"
      >
        {instructions ? (
          <>
            <KeyValueGrid
              items={[
                { label: "Số hóa đơn", value: instructions.invoice.number },
                { label: "Số tiền", value: vnd(instructions.invoice.amount) },
              ]}
            />
            <p style={{ whiteSpace: "pre-line", marginTop: 10 }}>{instructions.instructions}</p>
          </>
        ) : null}
      </CommitDialog>
    </Card>
  );
}
