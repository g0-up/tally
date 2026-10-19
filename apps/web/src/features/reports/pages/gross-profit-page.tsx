import { num } from "../../../shared/lib/format";
import { PageHeader } from "../../../shared/ui/page-header";
import { useReport } from "../api/queries";
import { ReportResult } from "../components/report-result";
import { ReportSummary } from "../components/report-summary";
import { ReportFilter } from "../containers/report-filter";
import type { Metric } from "../schemas";
import { useExportJob } from "../use-export-job";
import { useReportFilter } from "../use-report-filter";

const COLUMNS = ["name", "net_revenue", "cogs", "gross_margin", "margin_percent"] as const;
/** The KPI cards; the margin rate is the line under "Lãi gộp". */
const KPIS = ["net_revenue", "cogs", "gross_margin"];

function GrossMarginKpis({ metrics }: { metrics: readonly Metric[] }) {
  const rate = metrics.find((m) => m.key === "margin_percent")?.value;
  return (
    <ReportSummary
      metrics={metrics.filter((m) => KPIS.includes(m.key))}
      sub={(m) => (m.key === "gross_margin" && rate ? `Biên ${num(rate)}%` : undefined)}
    />
  );
}

/** Lãi gộp & kết quả quản trị (/reports/gross-profit). */
export default function GrossProfitPage() {
  const filter = useReportFilter({ range: "period", columns: COLUMNS });
  const report = useReport("gross_margin", filter.params);
  const exportJob = useExportJob("gross_margin");
  return (
    <>
      <PageHeader lead="Giá vốn bình quân di động, chụp tại lúc ghi sổ xuất. Combo ghi doanh thu cho combo, giá vốn là tổng thành phần; không cộng trùng." />
      <ReportFilter reportKey="gross_margin" filter={filter} exportJob={exportJob} />
      <ReportResult report={report} caption="Lãi gộp theo hàng" invalid={filter.invalid} summary={(first) => <GrossMarginKpis metrics={first.summary} />} />
    </>
  );
}
