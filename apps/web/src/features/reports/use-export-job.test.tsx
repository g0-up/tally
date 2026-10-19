import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { platformKeys } from "../../shared/api/query-keys";
import { apiError, apiUrl } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { renderWithProviders } from "../../test/render";
import { EXPORT_STATUS_LABELS, REPORT_MESSAGES } from "./api/adapters";
import { ExportButton, ExportPanel } from "./components/export-panel";
import { resetStoredExports, setStoredExport } from "./export-job-store";
import type { ExportJob } from "./schemas";
import { exportJob } from "./testing/fixtures";
import { useExportJob, type ExportJobOptions } from "./use-export-job";

const PATH = "/api/v1/export-jobs/exp-1/download";
const PARAMS = { from: "2026-10-01", to: "2026-10-12" };

function Harness(options: ExportJobOptions) {
  const state = useExportJob("nxt", options);
  return (
    <>
      <ExportButton state={state} params={PARAMS} />
      <ExportPanel state={state} />
    </>
  );
}

/** The export screen with a way to leave it and come back, under the same query cache. */
function Leaving(options: ExportJobOptions) {
  const [shown, setShown] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setShown((v) => !v)}>
        {shown ? "Rời màn hình" : "Quay lại"}
      </button>
      {shown ? <Harness {...options} /> : null}
    </>
  );
}

afterEach(() => {
  // Unmount first: a screen still shown would pick the next test's empty store up as a lost one.
  cleanup();
  resetStoredExports();
});

/** `POST /export-jobs` answers `created`; each `GET /export-jobs/exp-1` answers the next of `reads` (the last repeats). */
function exportApi(created: ExportJob, reads: readonly ExportJob[]) {
  const calls = { posts: [] as unknown[], gets: 0, cancels: 0 };
  server.use(
    http.post(apiUrl("/export-jobs"), async ({ request }) => {
      calls.posts.push(await request.json());
      return HttpResponse.json(created, { status: 202 });
    }),
    http.get(apiUrl("/export-jobs/exp-1"), () => {
      const job = reads[Math.min(calls.gets, reads.length - 1)];
      calls.gets += 1;
      return HttpResponse.json(job);
    }),
  );
  return calls;
}

const panel = () => screen.getByRole("status", { name: "Tệp xuất CSV" });

/** `GET /export-jobs` answers `jobs`, newest first; returns how often the list was read. */
function recentExports(jobs: readonly ExportJob[]) {
  const reads = { count: 0 };
  server.use(
    http.get(apiUrl("/export-jobs"), () => {
      reads.count += 1;
      return HttpResponse.json({ data: jobs, next_cursor: null });
    }),
  );
  return reads;
}

describe("useExportJob", () => {
  it("polls a queued export until it is written and opens the file only after a fresh read", async () => {
    const done = exportJob({ status: "succeeded", row_count: 1240, finished_at: "2026-10-12T02:01:00Z", expires_at: "2026-10-13T02:01:00Z", download_path: PATH });
    const calls = exportApi(exportJob(), [exportJob({ status: "running" }), done]);
    const open = vi.fn();
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} open={open} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.queued)).toBeInTheDocument();
    expect(calls.posts).toEqual([{ report_key: "nxt", params: PARAMS }]);
    expect(screen.getByRole("button", { name: "Xuất CSV" })).toBeDisabled();
    expect(await screen.findByText(EXPORT_STATUS_LABELS.running)).toBeInTheDocument();
    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    expect(within(panel()).getByText("1.240 dòng")).toBeInTheDocument();
    const link = within(panel()).getByRole("button", { name: "Tải tệp CSV" });
    // No link a middle click could open without the fresh read.
    expect(within(panel()).queryByRole("link")).not.toBeInTheDocument();

    // A succeeded export is terminal: no more reads.
    const reads = calls.gets;
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.gets).toBe(reads);

    await userEvent.click(link);
    await waitFor(() => expect(open).toHaveBeenCalledWith(PATH));
    expect(calls.gets).toBe(reads + 1);
    expect(screen.getByRole("button", { name: "Xuất CSV" })).toBeEnabled();
  });

  it("shows why an export failed", async () => {
    exportApi(exportJob(), [exportJob({ status: "failed", error: "Báo cáo quá lớn để xuất." })]);
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.failed)).toBeInTheDocument();
    // The reason is one alert of its own, not a polite line inside the state region.
    expect(screen.getByRole("alert")).toHaveTextContent("Báo cáo quá lớn để xuất.");
    expect(within(panel()).queryByText("Báo cáo quá lớn để xuất.")).not.toBeInTheDocument();
    expect(within(panel()).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(panel()).queryByRole("button", { name: "Tải tệp CSV" })).not.toBeInTheDocument();
    expect(within(panel()).queryByRole("button", { name: "Hủy" })).not.toBeInTheDocument();
  });

  it("cancels a queued export and stops reading its state", async () => {
    const cancelled = exportJob({ status: "cancelled", finished_at: "2026-10-12T02:00:30Z" });
    const calls = exportApi(exportJob(), [exportJob()]);
    server.use(
      http.post(apiUrl("/export-jobs/exp-1:cancel"), () => {
        calls.cancels += 1;
        server.use(http.get(apiUrl("/export-jobs/exp-1"), () => HttpResponse.json(cancelled)));
        return HttpResponse.json(cancelled);
      }),
    );
    renderWithProviders(<Harness intervalMs={10} maxPolls={50} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await userEvent.click(await within(await screen.findByRole("status", { name: "Tệp xuất CSV" })).findByRole("button", { name: "Hủy" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.cancelled)).toBeInTheDocument();
    expect(calls.cancels).toBe(1);
    const reads = calls.gets;
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.gets).toBe(reads);
  });

  it("says when the cancel came too late", async () => {
    exportApi(exportJob(), [exportJob({ status: "running" })]);
    server.use(http.post(apiUrl("/export-jobs/exp-1:cancel"), () => apiError(409, "export_status_invalid")));
    renderWithProviders(<Harness intervalMs={10} maxPolls={50} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await userEvent.click(await within(await screen.findByRole("status", { name: "Tệp xuất CSV" })).findByRole("button", { name: "Hủy" }));
    expect(await screen.findByText(REPORT_MESSAGES.cancelTooLate)).toBeInTheDocument();
  });

  it("explains a full export queue and starts nothing", async () => {
    let reads = 0;
    server.use(
      http.post(apiUrl("/export-jobs"), () => apiError(409, "export_queue_full")),
      http.get(apiUrl("/export-jobs/exp-1"), () => {
        reads += 1;
        return HttpResponse.json(exportJob());
      }),
    );
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(REPORT_MESSAGES.queueFull);
    expect(screen.getByRole("button", { name: "Xuất CSV" })).toBeEnabled();
    expect(reads).toBe(0);
  });

  it("refuses a file that expired since it was listed", async () => {
    const done = exportJob({ status: "succeeded", row_count: 10, expires_at: "2026-10-13T02:01:00Z", download_path: PATH });
    const calls = exportApi(exportJob(), [done, exportJob({ status: "expired", row_count: 10, expires_at: "2026-10-13T02:01:00Z" })]);
    const open = vi.fn();
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} open={open} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await userEvent.click(await screen.findByRole("button", { name: "Tải tệp CSV" }));
    expect(await screen.findByText(REPORT_MESSAGES.exportExpired)).toBeInTheDocument();
    expect(screen.getByText(EXPORT_STATUS_LABELS.expired)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Tải tệp CSV" })).not.toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
    expect(calls.gets).toBe(2);
  });

  it("stops after a bounded number of reads and resumes on “Kiểm tra lại”", async () => {
    const calls = exportApi(exportJob(), [exportJob({ status: "running" })]);
    renderWithProviders(<Harness intervalMs={10} maxPolls={4} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByText(REPORT_MESSAGES.exportStalled)).toBeInTheDocument();
    // The created job counts as the first read of the round.
    expect(calls.gets).toBe(3);
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.gets).toBe(3);

    server.use(http.get(apiUrl("/export-jobs/exp-1"), () => HttpResponse.json(exportJob({ status: "succeeded", row_count: 3, download_path: PATH }))));
    await userEvent.click(screen.getByRole("button", { name: "Kiểm tra lại" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    expect(screen.queryByText(REPORT_MESSAGES.exportStalled)).not.toBeInTheDocument();
  });

  it("stops reading the state once the screen is left", async () => {
    const calls = exportApi(exportJob(), [exportJob({ status: "running" })]);
    const view = renderWithProviders(<Harness intervalMs={10} maxPolls={1000} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await screen.findByText(EXPORT_STATUS_LABELS.running);
    view.unmount();
    const reads = calls.gets;
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.gets).toBe(reads);
  });
  it("follows the same export again when the screen is shown again", async () => {
    const calls = exportApi(exportJob(), [exportJob({ status: "running" })]);
    renderWithProviders(<Leaving intervalMs={10} maxPolls={1000} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await screen.findByText(EXPORT_STATUS_LABELS.running);
    await userEvent.click(screen.getByRole("button", { name: "Rời màn hình" }));
    const reads = calls.gets;
    await new Promise((r) => setTimeout(r, 60));
    expect(calls.gets).toBe(reads);

    server.use(http.get(apiUrl("/export-jobs/exp-1"), () => {
      calls.gets += 1;
      return HttpResponse.json(exportJob({ status: "succeeded", row_count: 7, download_path: PATH }));
    }));
    await userEvent.click(screen.getByRole("button", { name: "Quay lại" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    expect(within(panel()).getByRole("button", { name: "Tải tệp CSV" })).toBeInTheDocument();
    expect(calls.gets).toBeGreaterThan(reads);
    // The same export: nothing was started again.
    expect(calls.posts).toHaveLength(1);
  });

  it("forgets the previous export when a new one starts", async () => {
    const calls = exportApi(exportJob(), [exportJob({ status: "succeeded", row_count: 2, download_path: PATH })]);
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    server.use(http.post(apiUrl("/export-jobs"), () => apiError(409, "export_queue_full")));
    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(REPORT_MESSAGES.queueFull);
    expect(screen.queryByText(EXPORT_STATUS_LABELS.succeeded)).not.toBeInTheDocument();
    expect(calls.posts).toHaveLength(1);
  });

  it("stops polling on an expired session and hands it to the app's session handling", async () => {
    let reads = 0;
    server.use(
      http.post(apiUrl("/export-jobs"), () => HttpResponse.json(exportJob(), { status: 202 })),
      http.get(apiUrl("/export-jobs/exp-1"), () => {
        reads += 1;
        return apiError(401, "unauthenticated");
      }),
    );
    const { platform } = renderWithProviders(<Harness intervalMs={10} maxPolls={50} />);
    const invalidate = vi.spyOn(platform.queryClient, "invalidateQueries");

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: platformKeys.me() }));
    await new Promise((r) => setTimeout(r, 60));
    expect(reads).toBe(1);
  });

  it.each([
    [403, "forbidden", "Phiên này không được thực hiện thao tác này."],
    [404, "not_found", REPORT_MESSAGES.exportGone],
    [410, "export_expired", REPORT_MESSAGES.exportExpired],
  ])("stops polling on a %i and says why", async (status, code, shown) => {
    let reads = 0;
    server.use(
      http.post(apiUrl("/export-jobs"), () => HttpResponse.json(exportJob(), { status: 202 })),
      http.get(apiUrl("/export-jobs/exp-1"), () => {
        reads += 1;
        return apiError(status, code, { message: "Something went wrong" });
      }),
    );
    renderWithProviders(<Harness intervalMs={10} maxPolls={50} />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(shown);
    await new Promise((r) => setTimeout(r, 60));
    expect(reads).toBe(1);
    expect(screen.queryByRole("button", { name: "Kiểm tra lại" })).not.toBeInTheDocument();
  });

  it("picks up the newest export of the report the server is still writing when the tab kept none", async () => {
    const list = recentExports([
      exportJob({ id: "exp-9", kind: "full_data_export", report_key: "", params: {}, status: "running" }),
      exportJob({ id: "exp-8", report_key: "revenue", status: "running" }),
      exportJob({ id: "exp-1", status: "running" }),
      exportJob({ id: "exp-0", status: "succeeded", row_count: 3, download_path: "/api/v1/export-jobs/exp-0/download" }),
    ]);
    const calls = exportApi(exportJob(), [exportJob({ status: "succeeded", row_count: 12, download_path: PATH })]);
    const open = vi.fn();
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} open={open} />);

    expect(await screen.findByText(EXPORT_STATUS_LABELS.running)).toBeInTheDocument();
    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    expect(within(panel()).getByText("12 dòng")).toBeInTheDocument();
    await userEvent.click(within(panel()).getByRole("button", { name: "Tải tệp CSV" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(PATH));
    expect(list.count).toBe(1);
    expect(calls.posts).toHaveLength(0);
  });

  it("picks up a written export only while it can still be downloaded", async () => {
    const list = recentExports([
      exportJob({ id: "exp-3", status: "expired" }),
      exportJob({ id: "exp-2", status: "failed", error: "Báo cáo quá lớn để xuất." }),
      exportJob({ id: "exp-1", status: "succeeded", row_count: 4, download_path: null }),
      exportJob({ id: "exp-0", status: "cancelled" }),
    ]);
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} />);

    await waitFor(() => expect(list.count).toBe(1));
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByRole("status", { name: "Tệp xuất CSV" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Xuất CSV" })).toBeEnabled();
  });

  it("follows the export the tab kept without asking the server's list", async () => {
    setStoredExport("nxt", { id: "exp-1", round: 0 });
    const list = recentExports([exportJob({ id: "exp-2", status: "running" })]);
    exportApi(exportJob(), [exportJob({ status: "succeeded", row_count: 5, download_path: PATH })]);
    renderWithProviders(<Harness intervalMs={10} maxPolls={20} />);

    expect(await screen.findByText(EXPORT_STATUS_LABELS.succeeded)).toBeInTheDocument();
    expect(within(panel()).getByText("5 dòng")).toBeInTheDocument();
    expect(list.count).toBe(0);
  });

  it("does not bring back an earlier export after a new one was refused", async () => {
    const list = recentExports([]);
    server.use(http.post(apiUrl("/export-jobs"), () => apiError(409, "export_queue_full")));
    renderWithProviders(<Leaving intervalMs={10} maxPolls={20} />);

    await waitFor(() => expect(list.count).toBe(1));
    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(REPORT_MESSAGES.queueFull);

    const later = recentExports([exportJob({ id: "exp-1", status: "running" })]);
    await userEvent.click(screen.getByRole("button", { name: "Rời màn hình" }));
    await userEvent.click(screen.getByRole("button", { name: "Quay lại" }));
    await new Promise((r) => setTimeout(r, 30));
    expect(later.count).toBe(0);
    expect(screen.queryByRole("status", { name: "Tệp xuất CSV" })).not.toBeInTheDocument();
  });

  it("picks up each ledger's own export of a report", async () => {
    function Both() {
      const receivable = useExportJob("debt_aging", { variant: "receivable", intervalMs: 10, maxPolls: 20 });
      const payable = useExportJob("debt_aging", { variant: "payable", intervalMs: 10, maxPolls: 20 });
      return (
        <>
          <p>Phải thu: {receivable.job ? receivable.job.id : "chưa xuất"}</p>
          <p>Phải trả: {payable.job ? payable.job.id : "chưa xuất"}</p>
        </>
      );
    }
    const list = recentExports([
      exportJob({ id: "exp-5", report_key: "debt_aging", params: { as_of: "2026-10-12", direction: "payable" }, status: "running" }),
      exportJob({ id: "exp-4", report_key: "debt_aging", params: { as_of: "2026-10-11", direction: "payable" }, status: "running" }),
    ]);
    server.use(http.get(apiUrl("/export-jobs/exp-5"), () => HttpResponse.json(exportJob({ id: "exp-5", report_key: "debt_aging", status: "running" }))));
    renderWithProviders(<Both />);

    expect(await screen.findByText("Phải trả: exp-5")).toBeInTheDocument();
    expect(screen.getByText("Phải thu: chưa xuất")).toBeInTheDocument();
    // Both ledgers share one read of the list.
    expect(list.count).toBe(1);
  });

  it("keeps the two ledgers of one report on separate exports", async () => {
    function Both() {
      const receivable = useExportJob("debt_aging", { variant: "receivable", intervalMs: 10, maxPolls: 20 });
      const payable = useExportJob("debt_aging", { variant: "payable", intervalMs: 10, maxPolls: 20 });
      return (
        <>
          <ExportButton state={receivable} params={PARAMS} />
          <p>Phải trả: {payable.job ? payable.job.id : "chưa xuất"}</p>
        </>
      );
    }
    exportApi(exportJob(), [exportJob({ status: "running" })]);
    renderWithProviders(<Both />);

    await userEvent.click(screen.getByRole("button", { name: "Xuất CSV" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Xuất CSV" })).toBeDisabled());
    expect(screen.getByText("Phải trả: chưa xuất")).toBeInTheDocument();
  });
});
