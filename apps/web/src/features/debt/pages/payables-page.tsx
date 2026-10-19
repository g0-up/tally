import { useState } from "react";
import { useServerBusinessDate } from "../../../shared/api/platform-queries";
import { Button } from "../../../shared/ui/button";
import { PageHeader } from "../../../shared/ui/page-header";
import { DebtLedgerView } from "../containers/debt-ledger-view";

/** Cửa hàng còn nợ NCC (/debts/payables). `?party=<id>` opens the supplier's ledger. */
export default function PayablesPage() {
  const asOf = useServerBusinessDate();
  const [paying, setPaying] = useState(false);
  return (
    <>
      <PageHeader
        lead="Phải trả phát sinh khi phiếu nhập được ghi sổ. Thanh toán trước khi nhận là ứng trước, tự phân bổ khi nhập."
        actions={
          <Button variant="primary" onClick={() => setPaying(true)}>
            Trả tiền NCC
          </Button>
        }
      />
      <DebtLedgerView direction="payable" asOf={asOf} payRequested={paying} onPayClosed={() => setPaying(false)} />
    </>
  );
}
