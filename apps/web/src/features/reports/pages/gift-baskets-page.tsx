import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { PageHeader } from "../../../shared/ui/page-header";
import { formatCell } from "../api/adapters";
import { useReport } from "../api/queries";
import { ReportResult } from "../components/report-result";
import { ReportFilter } from "../containers/report-filter";
import type { Metric } from "../schemas";
import { useExportJob } from "../use-export-job";
import { useReportFilter } from "../use-report-filter";

const METRIC_COLUMNS: Column<Metric>[] = [
  { key: "label", header: "Chỉ tiêu", cell: (m) => m.label },
  { key: "value", header: "Giá trị", numeric: true, cell: (m) => formatCell(m.kind, m.value) },
];

/** Giỏ quà (/reports/gift-baskets). */
export default function GiftBasketsPage() {
  const filter = useReportFilter({ range: "period" });
  const report = useReport("kits", filter.params);
  const exportJob = useExportJob("kits");
  return (
    <>
      <PageHeader lead="Doanh thu giỏ ghi cho giỏ; thành phần đã tiêu hao lúc đóng nên không cộng doanh thu hay xuất kho lần nữa." />
      <ReportFilter reportKey="kits" filter={filter} exportJob={exportJob} />
      <ReportResult
        report={report}
        caption="Giỏ quà theo mã"
        invalid={filter.invalid}
        summary={(first) => <DataTable caption="Chỉ tiêu giỏ quà trong kỳ" columns={METRIC_COLUMNS} rows={first.summary} rowKey={(m) => m.key} />}
      />
    </>
  );
}
