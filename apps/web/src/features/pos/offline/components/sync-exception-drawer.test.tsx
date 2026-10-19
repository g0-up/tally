import { act, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { apiError } from "../../../../test/msw/handlers";
import { server } from "../../../../test/msw/server";
import { renderWithProviders } from "../../../../test/render";
import { addToOutbox, listOutbox } from "../outbox-repository";
import { parkedSales, type ParkedSaleView } from "../parked-sales";
import { resetOfflineDb } from "../testing/fake-offline-db";
import { OFFLINE_TENANT_ID, exceptionActionHandlers, testOutboxEntry, testSyncException } from "../testing/handlers";
import { NOT_ACTIONABLE_TITLE, SyncExceptionDrawer } from "./sync-exception-drawer";

/** One sale of this device parked by the server (sku deactivated meanwhile), stored in the outbox. */
async function parkedOnDevice() {
  const { label: _l, seq: _s, status: _st, exception: _e, ...entry } = testOutboxEntry(1);
  const stored = await addToOutbox(entry);
  const marked = { ...stored, status: "exception" as const, exception: { id: "sx-1", error_code: "sku_inactive", error_detail: "sku is inactive" } };
  return { entry: marked, parked: parkedSales([testSyncException(marked, { id: "sx-1" })], [marked]) };
}

function rowOf(label: string): HTMLElement {
  const row = screen.getByRole("cell", { name: label }).closest("tr");
  if (!row) throw new Error(`row ${label} missing`);
  return row;
}

beforeEach(resetOfflineDb);

describe("SyncExceptionDrawer", () => {
  it("shows each parked sale with time, goods, cash and reason", async () => {
    const { parked } = await parkedOnDevice();
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={parked} tenantId={OFFLINE_TENANT_ID} />);
    expect(await screen.findByText("Hàng đợi ngoại lệ (1)")).toBeInTheDocument();
    const row = rowOf("OFF-001");
    expect(row).toHaveTextContent("Kẹo dừa × 2 Gói");
    expect(row).toHaveTextContent("100.000");
  });

  it("“Ghi sổ lại” posts the sale as parked, once, and drops it from the device", async () => {
    const user = userEvent.setup();
    const { entry, parked } = await parkedOnDevice();
    const actions = exceptionActionHandlers({ docNo: "BH-0042" });
    server.use(actions.handler);
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={parked} tenantId={OFFLINE_TENANT_ID} />);

    await user.click(await within(rowOf("OFF-001")).findByRole("button", { name: "Ghi sổ lại OFF-001" }));

    expect(await screen.findByText(/Đã ghi sổ OFF-001 thành/)).toBeInTheDocument();
    expect(actions.resolved).toHaveLength(1);
    expect(actions.resolved[0]).toMatchObject({ id: "sx-1", body: { lines: entry.sale.lines, payments: entry.sale.payments, cash_tendered_amount: "100000" } });
    expect(actions.resolved[0]?.idempotencyKey).toBeTruthy();
    await waitFor(async () => expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]));
  });

  it("explains a sale settled meanwhile and keeps it on the device until the queue says so", async () => {
    const user = userEvent.setup();
    const { parked } = await parkedOnDevice();
    const actions = exceptionActionHandlers({ refuse: () => apiError(409, "sync_exception_closed") });
    server.use(actions.handler);
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={parked} tenantId={OFFLINE_TENANT_ID} />);

    await user.click(await within(rowOf("OFF-001")).findByRole("button", { name: "Ghi sổ lại OFF-001" }));

    expect(await screen.findByText(/Giao dịch đã được xử lý\./)).toBeInTheDocument();
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });

  it("“Bỏ qua giao dịch” asks for confirmation, then closes the sale without a document", async () => {
    const user = userEvent.setup();
    const { parked } = await parkedOnDevice();
    const actions = exceptionActionHandlers();
    server.use(actions.handler);
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={parked} tenantId={OFFLINE_TENANT_ID} />);

    await user.click(await within(rowOf("OFF-001")).findByRole("button", { name: "Bỏ qua giao dịch OFF-001" }));
    const dialog = await screen.findByRole("dialog", { name: "Bỏ qua OFF-001?" });
    expect(dialog).toHaveTextContent("Tiền đã thu của giao dịch này cần được đối chiếu ngoài hệ thống.");
    expect(actions.discarded).toEqual([]);

    await user.click(within(dialog).getByRole("button", { name: "Bỏ qua giao dịch" }));

    expect(await screen.findByText("Đã bỏ qua OFF-001. Giao dịch không được ghi sổ.")).toBeInTheDocument();
    expect(actions.discarded).toEqual(["sx-1"]);
    expect(actions.resolved).toEqual([]);
    await waitFor(async () => expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]));
  });

  it("disables both actions for a sale the server queue was not read for and states why on screen", async () => {
    const { entry } = await parkedOnDevice();
    const offlineView = parkedSales(undefined, [{ ...entry, exception: { id: null, error_code: "sku_inactive", error_detail: null } }]);
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={offlineView} tenantId={OFFLINE_TENANT_ID} />);

    const row = await screen.findByRole("cell", { name: "OFF-001" });
    const tr = row.closest("tr") as HTMLElement;
    for (const name of ["Ghi sổ lại OFF-001", "Bỏ qua giao dịch OFF-001"]) {
      const button = within(tr).getByRole("button", { name });
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute("title", NOT_ACTIONABLE_TITLE);
      expect(button).toHaveAccessibleDescription(NOT_ACTIONABLE_TITLE);
    }
    expect(screen.getByText(NOT_ACTIONABLE_TITLE)).toBeVisible();
  });

  it("shows no reason line while every sale can be handled", async () => {
    const { parked } = await parkedOnDevice();
    renderWithProviders(<SyncExceptionDrawer open onClose={() => undefined} parked={parked} tenantId={OFFLINE_TENANT_ID} />);
    expect(await within(rowOf("OFF-001")).findByRole("button", { name: "Ghi sổ lại OFF-001" })).toBeEnabled();
    expect(screen.queryByText(NOT_ACTIONABLE_TITLE)).not.toBeInTheDocument();
  });

  it("moves focus to the drawer when the discarded sale leaves the queue", async () => {
    const user = userEvent.setup();
    const { parked } = await parkedOnDevice();
    const actions = exceptionActionHandlers();
    server.use(actions.handler);
    let setParked: (next: readonly ParkedSaleView[]) => void = () => {};
    function Harness() {
      const [rows, setRows] = useState<readonly ParkedSaleView[]>(parked);
      setParked = setRows;
      return <SyncExceptionDrawer open onClose={() => undefined} parked={rows} tenantId={OFFLINE_TENANT_ID} />;
    }
    renderWithProviders(<Harness />);

    await user.click(await within(rowOf("OFF-001")).findByRole("button", { name: "Bỏ qua giao dịch OFF-001" }));
    await user.click(within(await screen.findByRole("dialog", { name: "Bỏ qua OFF-001?" })).getByRole("button", { name: "Bỏ qua giao dịch" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Bỏ qua OFF-001?" })).not.toBeInTheDocument());

    // The queue and the outbox were re-read without the sale.
    act(() => setParked([]));
    expect(screen.getByRole("dialog", { name: "Hàng đợi ngoại lệ (0)" })).toHaveFocus();
  });
});
