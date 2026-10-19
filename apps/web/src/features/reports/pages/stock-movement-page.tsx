import { Link } from "react-router";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { stockCardHref } from "../../inventory";
import { useReport } from "../api/queries";
import type { CellLink } from "../components/report-table";
import { ReportResult } from "../components/report-result";
import { ReportFilter } from "../containers/report-filter";
import { useExportJob } from "../use-export-job";
import { useReportFilter } from "../use-report-filter";

/** The prototype's columns; the value columns of the opening and the flows stay in the export. */
const COLUMNS = ["code", "name", "opening_qty", "in_qty", "out_qty", "closing_qty", "closing_value"] as const;

/** A SKU code opens its stock card. */
const codeLink: CellLink = (column, row, text) => (column.key === "code" && row.id ? <Link to={stockCardHref(row.id)}>{text}</Link> : undefined);

/** Nhập–xuất–tồn (/reports/stock-movement). */
export default function StockMovementPage() {
  const filter = useReportFilter({ range: "period", columns: COLUMNS });
  const report = useReport("nxt", filter.params);
  const exportJob = useExportJob("nxt");
  return (
    <>
      <PageHeader lead="Đầu kỳ + nhập − xuất = cuối kỳ, tính từ sổ chuyển động theo đơn vị cơ sở. Bấm mã hàng để xem thẻ kho." />
      <ReportFilter reportKey="nxt" filter={filter} exportJob={exportJob}>
        {filter.to < filter.today ? <StatusPill label="Kỳ kết thúc trước hôm nay nên không so với tồn hiện tại" tone="mute" /> : null}
      </ReportFilter>
      <ReportResult report={report} caption="Nhập–xuất–tồn" invalid={filter.invalid} link={codeLink} />
    </>
  );
}
