import { useState } from "react";
import { useServerBusinessDate } from "../../../shared/api/platform-queries";
import { todayBusinessDate } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { PageHeader } from "../../../shared/ui/page-header";
import { DebtLedgerView } from "../containers/debt-ledger-view";

/** Khách còn nợ (/debts/receivables). `?party=<id>` opens the customer's ledger. */
export default function ReceivablesPage() {
  const asOf = useServerBusinessDate();
  const today = asOf ?? todayBusinessDate();
  const [paying, setPaying] = useState(false);
  return (
    <>
      <PageHeader
        lead={`Tuổi nợ tính theo hạn của phần chưa thanh toán, tại ngày ${fd(today)}. Tiền khách ứng trước trình bày riêng, không gộp thành nợ âm. Phải thu và phải trả của cùng một đối tác không tự bù trừ.`}
        actions={
          <Button variant="primary" onClick={() => setPaying(true)}>
            Thu tiền khách
          </Button>
        }
      />
      <DebtLedgerView direction="receivable" asOf={asOf} payRequested={paying} onPayClosed={() => setPaying(false)} />
    </>
  );
}
