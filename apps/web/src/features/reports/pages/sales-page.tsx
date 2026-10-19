import { PageHeader } from "../../../shared/ui/page-header";
import { useReport } from "../api/queries";
import { ReportResult } from "../components/report-result";
import { ReportSummary } from "../components/report-summary";
import { ReportFilter } from "../containers/report-filter";
import { useExportJob } from "../use-export-job";
import { useReportFilter } from "../use-report-filter";

const COLUMNS = ["name", "sales_amount", "returns_amount", "net_revenue"] as const;

const GROUP_BYS = [
  { value: "product", label: "Theo hàng" },
  { value: "customer", label: "Theo khách" },
  { value: "channel", label: "Theo kênh" },
] as const;

/** Bán hàng (/reports/sales). */
export default function SalesPage() {
  const filter = useReportFilter({ range: "period", groupBy: "product", columns: COLUMNS });
  const report = useReport("revenue", filter.params);
  const exportJob = useExportJob("revenue");
  return (
    <>
      <PageHeader lead="Doanh thu thực hiện theo chứng từ bán đã ghi sổ (xuất giao và POS), sau trả hàng. Không lấy tổng giá trị hóa đơn chưa giao làm doanh thu." />
      <ReportFilter reportKey="revenue" filter={filter} groupBys={GROUP_BYS} exportJob={exportJob} />
      <ReportResult report={report} caption="Doanh thu bán hàng" invalid={filter.invalid} summary={(first) => <ReportSummary metrics={first.summary} />} />
    </>
  );
}
