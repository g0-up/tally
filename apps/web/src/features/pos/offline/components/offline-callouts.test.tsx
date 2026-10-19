import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fdt } from "../../../../shared/lib/dates";
import { announceUpdate, dismissUpdate } from "../app-update";
import { connectivity } from "../connectivity";
import { OFFLINE_MESSAGES } from "../offline-sale-service";
import { parkedSales } from "../parked-sales";
import { PRICED_AT, testBundle, testOutboxEntry, testSyncException } from "../testing/handlers";
import { OfflineBanner } from "./offline-banner";
import { OfflineSimulationToggle } from "./offline-simulation-toggle";
import { foreignSalesMessage } from "../use-offline-sync";
import { BlockedSalesCallout } from "./blocked-sales-callout";
import { ConnectionStatus } from "./connection-status";
import { PENDING_STATUS } from "./pending-sync-list";
import { SyncExceptionsCallout } from "./sync-exceptions-callout";
import { SyncQueueCallout } from "./sync-queue-callout";
import { UpdatePrompt } from "./update-prompt";

const NOW = new Date("2026-10-18T09:00:00.000Z");

/** The offline banner callout; it is announced by the region `ConnectionStatus` puts it in. */
function banner(): HTMLElement {
  const callout = screen.getByText("Đang ngoại tuyến.").parentElement;
  if (callout === null) throw new Error("No offline banner");
  return callout;
}

afterEach(() => {
  connectivity.reset();
  act(() => dismissUpdate());
});

describe("OfflineBanner", () => {
  it("states the offline rules, the price time and the pending count, and lists the pending sales", async () => {
    const user = userEvent.setup();
    render(<OfflineBanner bundle={testBundle()} pending={[testOutboxEntry(1), testOutboxEntry(2)]} now={NOW} />);
    expect(banner()).toHaveTextContent(
      `Đang ngoại tuyến. Dùng bảng giá cache lúc ${fdt(PRICED_AT)}. Chỉ bán thu tiền ngay; không bán chịu, không hoàn tiền. Tồn hiển thị có thể chưa chính xác. Giao dịch chờ đồng bộ: 2.`,
    );
    expect(banner()).not.toHaveTextContent(OFFLINE_MESSAGES.expired);

    const toggle = screen.getByRole("button", { name: "Xem giao dịch chờ" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const table = screen.getByRole("table", { name: "Giao dịch chờ đồng bộ" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["OFF-001", "OFF-002"]);
    expect(within(rows[0] as HTMLElement).getByText(PENDING_STATUS)).toBeInTheDocument();
    expect(rows[0]).toHaveTextContent("100.000");

    await user.click(toggle);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("warns when the catalog expired, is missing or offline selling is off", () => {
    const { rerender } = render(<OfflineBanner bundle={testBundle()} pending={[]} now={new Date("2026-10-18T21:00:00Z")} />);
    expect(banner()).toHaveTextContent(OFFLINE_MESSAGES.expired);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();

    rerender(<OfflineBanner bundle={null} pending={[]} now={NOW} />);
    expect(banner()).toHaveTextContent(`Đang ngoại tuyến. ${OFFLINE_MESSAGES.noBundle} Giao dịch chờ đồng bộ: 0.`);

    rerender(<OfflineBanner bundle={testBundle({ settings: { ...testBundle().settings, offline_pos_enabled: false } })} pending={[]} now={NOW} />);
    expect(banner()).toHaveTextContent(OFFLINE_MESSAGES.disabled);

    rerender(<OfflineBanner bundle={testBundle({ settings: { ...testBundle().settings, prices_include_tax: false } })} pending={[]} now={NOW} />);
    expect(banner()).toHaveTextContent(OFFLINE_MESSAGES.taxExcluded);
  });
});

describe("ConnectionStatus", () => {
  it("keeps one polite live region on the page and fills it when the network drops or sales wait", () => {
    const props = { bundle: testBundle(), syncing: false, onSync: vi.fn(), foreign: 0 };
    const { container, rerender } = render(<ConnectionStatus {...props} offline={false} pending={[]} />);
    const region = container.querySelector('[aria-live="polite"]');
    if (!(region instanceof HTMLElement)) throw new Error("No live region");
    expect(region).toBeEmptyDOMElement();

    rerender(<ConnectionStatus {...props} offline pending={[testOutboxEntry(1)]} />);
    expect(container.querySelector('[aria-live="polite"]')).toBe(region);
    expect(region).toContainElement(banner());
    expect(region).toHaveTextContent("Giao dịch chờ đồng bộ: 1.");
    // A nested live region would be announced twice or not at all.
    expect(region.querySelector('[aria-live], [role="status"], [role="alert"]')).toBeNull();

    rerender(<ConnectionStatus {...props} offline={false} pending={[testOutboxEntry(1), testOutboxEntry(2)]} foreign={1} />);
    expect(container.querySelector('[aria-live="polite"]')).toBe(region);
    expect(region).toHaveTextContent("Có 2 giao dịch ngoại tuyến chờ đồng bộ.");
    expect(region).toHaveTextContent(foreignSalesMessage(1));
    expect(region.querySelector('[aria-live], [role="status"], [role="alert"]')).toBeNull();
  });
});

describe("SyncQueueCallout", () => {
  it("offers “Đồng bộ ngay” while sales wait, and nothing otherwise", async () => {
    const onSync = vi.fn();
    const { rerender } = render(<SyncQueueCallout count={3} syncing={false} onSync={onSync} />);
    expect(screen.getByText(/^Có 3 giao dịch ngoại tuyến chờ đồng bộ\./)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Đồng bộ ngay" }));
    expect(onSync).toHaveBeenCalledTimes(1);

    rerender(<SyncQueueCallout count={3} syncing onSync={onSync} />);
    const syncing = screen.getByRole("button", { name: /Đang đồng bộ…/ });
    expect(syncing).toHaveAttribute("aria-disabled", "true");
    await userEvent.click(syncing);
    expect(onSync).toHaveBeenCalledTimes(1);

    rerender(<SyncQueueCallout count={0} syncing={false} onSync={onSync} />);
    expect(screen.queryByText(/chờ đồng bộ/)).not.toBeInTheDocument();
  });
});

describe("SyncExceptionsCallout", () => {
  it("lists each parked sale with its reason and opens the queue", async () => {
    const mine = testOutboxEntry(1, { status: "exception" });
    const parked = parkedSales([testSyncException(mine, { error_code: "offline_amount_exceeded" })], [mine]);
    const onOpen = vi.fn();
    render(<SyncExceptionsCallout parked={parked} onOpen={onOpen} />);
    expect(screen.getByRole("alert")).toHaveTextContent(/Hàng đợi ngoại lệ \(1\):/);
    expect(screen.getByText(/Hàng đợi ngoại lệ \(1\):/).parentElement).toHaveTextContent(
      "Hàng đợi ngoại lệ (1): OFF-001: Vượt hạn mức bán ngoại tuyến.. Giao dịch không bị mất, quản lý cần xử lý.",
    );
    await userEvent.click(screen.getByRole("button", { name: "Xử lý" }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("renders nothing without parked sales", () => {
    const { container } = render(<SyncExceptionsCallout parked={[]} onOpen={() => undefined} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("BlockedSalesCallout", () => {
  const refused = { id: null, error_code: "validation_failed", error_detail: "Dòng hàng không hợp lệ." };
  const blocked = testOutboxEntry(4, { status: "blocked", exception: refused });

  it("lists each refused sale with its reason and sends it again on “Gửi lại”", async () => {
    const onRequeue = vi.fn().mockResolvedValue(undefined);
    render(<BlockedSalesCallout blocked={[blocked]} onRequeue={onRequeue} onDrop={vi.fn()} />);
    const callout = screen.getByRole("alert");
    expect(callout).toHaveTextContent(/Giao dịch ngoại tuyến không đồng bộ được \(1\):/);
    expect(within(callout).getByRole("listitem")).toHaveTextContent(/OFF-004 · 100\.000/);

    await userEvent.click(within(callout).getByRole("button", { name: "Gửi lại OFF-004" }));
    expect(onRequeue).toHaveBeenCalledWith(blocked.offline_client_id);
  });

  it("shows progress only on the sale being sent and keeps the others from acting meanwhile", async () => {
    const other = testOutboxEntry(5, { status: "blocked", exception: refused });
    let finish = () => {};
    const onRequeue = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    render(<BlockedSalesCallout blocked={[blocked, other]} onRequeue={onRequeue} onDrop={vi.fn()} />);

    await userEvent.click(screen.getByRole("button", { name: "Gửi lại OFF-004" }));
    const [first, second] = screen.getAllByRole("listitem") as [HTMLElement, HTMLElement];
    const sending = within(first).getByRole("button", { name: "Đang gửi…" });
    expect(sending).toHaveAttribute("aria-disabled", "true");
    expect(sending).toHaveFocus();
    await userEvent.click(sending);
    expect(onRequeue).toHaveBeenCalledTimes(1);
    expect(within(second).getByRole("button", { name: "Gửi lại OFF-005" })).toBeDisabled();
    expect(within(second).queryByText("Đang gửi…")).not.toBeInTheDocument();

    await act(async () => finish());
    expect(screen.getByRole("button", { name: "Gửi lại OFF-004" })).toBeEnabled();
  });

  it("moves focus to the next sale's button when the one sent again leaves the list", async () => {
    const other = testOutboxEntry(5, { status: "blocked", exception: refused });
    const onRequeue = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(<BlockedSalesCallout blocked={[blocked, other]} onRequeue={onRequeue} onDrop={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "Gửi lại OFF-004" }));
    expect(onRequeue).toHaveBeenCalledTimes(1);

    rerender(<BlockedSalesCallout blocked={[other]} onRequeue={onRequeue} onDrop={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Gửi lại OFF-005" })).toHaveFocus();
  });

  it("hands focus back to the sale's button when sending it again fails", async () => {
    let fail = () => {};
    const onRequeue = vi.fn(() => new Promise<void>((_, reject) => (fail = () => reject(new Error("Mất mạng")))));
    render(
      <>
        <input aria-label="Ô khác" />
        <BlockedSalesCallout blocked={[blocked]} onRequeue={onRequeue} onDrop={vi.fn()} />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Gửi lại OFF-004" }));
    // The cashier left the button while it was sending; focus is on <body> when the failure comes.
    const elsewhere = screen.getByRole("textbox", { name: "Ô khác" });
    act(() => {
      elsewhere.focus();
      elsewhere.blur();
    });
    expect(document.body).toHaveFocus();

    await act(async () => fail());
    expect(screen.getByRole("button", { name: "Gửi lại OFF-004" })).toHaveFocus();
  });

  it("removes a sale from the device only after the cashier confirms", async () => {
    const onDrop = vi.fn().mockResolvedValue(undefined);
    render(<BlockedSalesCallout blocked={[blocked]} onRequeue={vi.fn()} onDrop={onDrop} />);
    await userEvent.click(screen.getByRole("button", { name: "Xóa OFF-004 khỏi máy" }));
    const dialog = screen.getByRole("dialog", { name: "Xóa OFF-004 khỏi máy?" });
    expect(dialog).toHaveTextContent(/sẽ không bao giờ được đồng bộ sau khi xóa/);
    expect(onDrop).not.toHaveBeenCalled();

    await userEvent.click(within(dialog).getByRole("button", { name: "Xóa khỏi máy" }));
    expect(onDrop).toHaveBeenCalledWith(blocked.offline_client_id);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the dialog open when the removal fails", async () => {
    const onDrop = vi.fn().mockRejectedValue(new Error("IndexedDB lỗi"));
    render(<BlockedSalesCallout blocked={[blocked]} onRequeue={vi.fn()} onDrop={onDrop} />);
    await userEvent.click(screen.getByRole("button", { name: "Xóa OFF-004 khỏi máy" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Xóa khỏi máy" }));
    expect(onDrop).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "Xóa OFF-004 khỏi máy?" })).toBeInTheDocument();
  });

  it("renders nothing without blocked sales", () => {
    const { container } = render(<BlockedSalesCallout blocked={[]} onRequeue={vi.fn()} onDrop={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("OfflineSimulationToggle", () => {
  it("forces the offline state and hands it back", async () => {
    const { rerender } = render(<OfflineSimulationToggle simulated={false} />);
    await userEvent.click(screen.getByRole("button", { name: "Mô phỏng mất mạng" }));
    expect(connectivity.isSimulated()).toBe(true);
    expect(connectivity.getState()).toBe("offline");

    rerender(<OfflineSimulationToggle simulated />);
    await userEvent.click(screen.getByRole("button", { name: "Kết nối lại mạng" }));
    expect(connectivity.isSimulated()).toBe(false);
  });
});

describe("UpdatePrompt", () => {
  it("appears when a new version waits and reloads into it", async () => {
    const apply = vi.fn().mockResolvedValue(undefined);
    render(<UpdatePrompt />);
    const region = screen.getByRole("status");
    expect(region).toBeEmptyDOMElement();

    act(() => announceUpdate(apply));
    expect(screen.getByRole("status")).toBe(region);
    expect(screen.getByRole("status")).toHaveTextContent("Đã có phiên bản mới. Tải lại để cập nhật.");
    await userEvent.click(screen.getByRole("button", { name: "Tải lại" }));
    expect(apply).toHaveBeenCalledTimes(1);
  });

  it("shows why the reload failed and can be put off", async () => {
    render(<UpdatePrompt />);
    act(() => announceUpdate(() => Promise.reject(new Error("Service worker lỗi"))));
    await userEvent.click(screen.getByRole("button", { name: "Tải lại" }));
    expect(await screen.findByText(/Chưa tải lại được: Service worker lỗi/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Để sau" }));
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
  });
});
