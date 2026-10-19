import { fd } from "../../../shared/lib/format";
import { Card } from "../../../shared/ui/card";
import { PageHeader } from "../../../shared/ui/page-header";
import { useReport } from "../api/queries";
import { ExportButton, ExportPanel } from "../components/export-panel";
import { ReportResult } from "../components/report-result";
import { ReportSummary } from "../components/report-summary";
import { ReportFilter } from "../containers/report-filter";
import { useExportJob } from "../use-export-job";
import { useReportFilter, type ReportFilter as Filter } from "../use-report-filter";

type Direction = "receivable" | "payable";

const TITLES: Readonly<Record<Direction, string>> = { receivable: "Phải thu khách", payable: "Phải trả nhà cung cấp" };

/** One ledger's aging with its own export: the server reports one direction per run. */
function AgingSection({ direction, filter }: { direction: Direction; filter: Filter }) {
  const params = filter.params === null ? null : { ...filter.params, direction };
  const report = useReport("debt_aging", params);
  const exportJob = useExportJob("debt_aging", { variant: direction });
  return (
    <Card title={TITLES[direction]} actions={<ExportButton state={exportJob} params={params} />}>
      <ExportPanel state={exportJob} />
      <ReportResult report={report} caption={TITLES[direction]} summary={(first) => <ReportSummary metrics={first.summary} />} />
    </Card>
  );
}

/** Tuổi nợ (/reports/debt-aging). */
export default function DebtAgingPage() {
  const filter = useReportFilter({ range: "as_of" });
  return (
    <>
      <PageHeader lead={`Tính tại ngày ${fd(filter.asOf)} theo hạn của phần chưa thanh toán. Ứng trước trình bày ở màn hình riêng, không gộp thành nợ âm.`} />
      <ReportFilter reportKey="debt_aging" filter={filter} />
      <AgingSection direction="receivable" filter={filter} />
      <AgingSection direction="payable" filter={filter} />
    </>
  );
}
