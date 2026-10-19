import { z } from "zod";
import type { components } from "../../shared/api/schema";

type S = components["schemas"];

// Response schemas of the reporting endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

export const REPORT_KEYS = [
  "nxt",
  "lot_stock",
  "revenue",
  "gross_margin",
  "debt_aging",
  "debt_schedule",
  "cash_flow",
  "kits",
  "season",
  "buy_suggestions",
  "clearance_suggestions",
  "cash_forecast",
] as const;
export type ReportKey = (typeof REPORT_KEYS)[number];

export const columnKindSchema = z.enum(["text", "money", "qty", "percent", "date", "int"]);
export type ColumnKind = z.infer<typeof columnKindSchema>;

export const reportColumnSchema = z.object({ key: z.string(), label: z.string(), kind: columnKindSchema });
export type ReportColumn = z.infer<typeof reportColumnSchema>;

/** Cells by column key: a decimal or date string, or null. `id` names what the row is about. */
export const reportRowSchema = z.record(z.string(), z.string().nullable());
export type ReportRow = z.infer<typeof reportRowSchema>;

export const metricSchema = z.object({
  key: z.string(),
  label: z.string(),
  kind: columnKindSchema,
  value: z.string().nullable(),
  previous: z.string().nullish(),
});
export type Metric = z.infer<typeof metricSchema>;

export const sectionSchema = z.object({
  key: z.string(),
  title: z.string(),
  columns: z.array(reportColumnSchema),
  rows: z.array(reportRowSchema),
  totals: reportRowSchema.nullish(),
});
export type ReportSection = z.infer<typeof sectionSchema>;

/** One page of `GET /reports/{reportKey}`. */
export const reportTableSchema = z.object({
  report_key: z.enum(REPORT_KEYS),
  title: z.string(),
  from: z.string().nullable(),
  to: z.string().nullable(),
  as_of: z.string().nullable(),
  group_by: z.string().nullable(),
  scope: z.string(),
  notes: z.array(z.string()),
  provisional: z.boolean(),
  columns: z.array(reportColumnSchema),
  rows: z.array(reportRowSchema),
  /** Totals of the whole result; the stock reports send them on the first page only. */
  totals: reportRowSchema.nullable(),
  summary: z.array(metricSchema),
  sections: z.array(sectionSchema),
  next_cursor: z.string().nullable(),
});
export type ReportTable = z.infer<typeof reportTableSchema>;

export const reportConfigSchema = z.object({
  columns: z.array(z.string()).nullish(),
  group_by: z.string().nullish(),
  filters: z.record(z.string(), z.string()).nullish(),
});
export type ReportConfig = z.infer<typeof reportConfigSchema>;

export const savedReportSchema = z.object({
  id: z.string(),
  report_key: z.enum(REPORT_KEYS),
  name: z.string(),
  config: reportConfigSchema,
  updated_at: z.string(),
});
export type SavedReport = z.infer<typeof savedReportSchema>;

export const exportStatusSchema = z.enum(["queued", "running", "succeeded", "failed", "expired", "cancelled"]);
export type ExportStatus = z.infer<typeof exportStatusSchema>;

export const exportJobSchema = z.object({
  id: z.string(),
  kind: z.enum(["report_csv", "full_data_export"]),
  /** Empty for a full data export. */
  report_key: z.union([z.enum(REPORT_KEYS), z.literal("")]),
  /** The report parameters the export was started with. */
  params: z.record(z.string(), z.string()),
  status: exportStatusSchema,
  row_count: z.number().int().nullable(),
  error: z.string().nullable(),
  created_at: z.string(),
  finished_at: z.string().nullable(),
  expires_at: z.string().nullable(),
  /** Set while a finished export can be downloaded. */
  download_path: z.string().nullable(),
}) satisfies z.ZodType<Omit<S["ExportJob"], "started_at">>;
export type ExportJob = z.infer<typeof exportJobSchema>;
