import type { ReactNode } from "react";
import { DatePicker } from "../../../shared/ui/date-picker";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ToggleGroup, type ToggleOption } from "../../../shared/ui/toggle-group";
import { REPORT_TEXT } from "../api/adapters";
import { ExportButton, ExportPanel } from "../components/export-panel";
import type { ReportKey } from "../schemas";
import type { ExportJobState } from "../use-export-job";
import type { ReportFilter as Filter } from "../use-report-filter";
import { SavedReportsMenu } from "./saved-reports-menu";

export interface ReportFilterProps {
  reportKey: ReportKey;
  filter: Filter;
  /** Groupings offered as chips ("Theo hàng", "Theo khách", "Theo kênh"). */
  groupBys?: ReadonlyArray<ToggleOption<string>>;
  /** "Xuất CSV" of this filter; screens that export per section leave it out. */
  exportJob?: ExportJobState;
  children?: ReactNode;
}

function DateField({ label, value, today, onChange }: { label: string; value: string; today: string; onChange: (iso: string) => void }) {
  return (
    <label className="f inline">
      <span>{label}</span>
      <DatePicker value={value} today={today} onChange={(v) => (v ? onChange(v) : undefined)} />
    </label>
  );
}

/** Prototype `repFilter`: the period or date, the provisional hint, saved layouts and "Xuất CSV". */
export function ReportFilter({ reportKey, filter, groupBys, exportJob, children }: ReportFilterProps) {
  return (
    <>
      <FilterBar>
        {filter.range === "period" ? (
          <>
            <DateField label={REPORT_TEXT.from} value={filter.from} today={filter.today} onChange={filter.setFrom} />
            <DateField label={REPORT_TEXT.to} value={filter.to} today={filter.today} onChange={filter.setTo} />
          </>
        ) : (
          <DateField label={REPORT_TEXT.asOf} value={filter.asOf} today={filter.today} onChange={filter.setAsOf} />
        )}
        {groupBys && filter.groupBy !== null ? <ToggleGroup label="Nhóm theo" options={groupBys} value={filter.groupBy} onChange={filter.setGroupBy} /> : null}
        {children}
        <span className="hint">{REPORT_TEXT.hint}</span>
        <SavedReportsMenu reportKey={reportKey} config={filter.config} onApply={filter.applyConfig} />
        {exportJob ? <ExportButton state={exportJob} params={filter.params} /> : null}
      </FilterBar>
      {exportJob ? <ExportPanel state={exportJob} /> : null}
    </>
  );
}
