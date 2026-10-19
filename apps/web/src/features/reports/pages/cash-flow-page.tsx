import { PageHeader } from "../../../shared/ui/page-header";
import { useReport } from "../api/queries";
import { ReportResult } from "../components/report-result";
import { ReportSummary } from "../components/report-summary";
import { ReportFilter } from "../containers/report-filter";
import { useExportJob } from "../use-export-job";
import { useReportFilter } from "../use-report-filter";

/** The prototype's KPI labels over the server's metrics. */
const LABELS = { opening: "Đầu kỳ", in_amount: "Tổng thu", out_amount: "Tổng chi", closing: "Cuối kỳ" };

/** Dòng tiền (/reports/cash-flow). */
export default function CashFlowPage() {
  const filter = useReportFilter({ range: "period" });
  const report = useReport("cash_flow", filter.params);
  const exportJob = useExportJob("cash_flow");
  return (
    <>
      <PageHeader lead="Tiền thực vào/ra quỹ theo phân loại. Thu tiền bán hàng, tiền cọc, góp vốn và chuyển quỹ được tách riêng." />
      <ReportFilter reportKey="cash_flow" filter={filter} exportJob={exportJob} />
      <ReportResult report={report} caption="Thu chi theo loại" invalid={filter.invalid} summary={(first) => <ReportSummary metrics={first.summary} labels={LABELS} />} />
    </>
  );
}
