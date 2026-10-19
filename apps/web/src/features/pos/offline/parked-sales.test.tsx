import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { foreignLabel, parkedSales } from "./parked-sales";
import { syncErrorText } from "./sync-errors";
import { testOutboxEntry, testSyncException } from "./testing/handlers";
import { notSavedMessage, savedOfflineMessage } from "./use-offline-checkout";
import { syncDoneMessage } from "./use-offline-sync";

const parked = (seq: number) => testOutboxEntry(seq, { status: "exception", exception: { id: `sx-${seq}`, error_code: "sku_inactive", error_detail: "sku is inactive" } });

describe("parkedSales", () => {
  it("joins the server queue with this device's labels and lines", () => {
    const mine = parked(1);
    const foreign = testOutboxEntry(9, { device_session_id: "other-session" });
    const rows = parkedSales([testSyncException(mine), testSyncException(foreign, { error_code: "offline_window_expired" })], [mine]);

    expect(rows[0]).toMatchObject({
      id: "sx-1",
      offlineClientId: mine.offline_client_id,
      label: "OFF-001",
      createdAt: mine.sale.created_at,
      lines: mine.lines,
      lineCount: 1,
      cash: "100000",
      body: { lines: mine.sale.lines, payments: mine.sale.payments, cash_tendered_amount: "100000" },
    });
    expect(rows[1]).toMatchObject({ label: foreignLabel(foreign.offline_client_id), lines: null, lineCount: 1, error: "Bán ngoài thời gian cho phép không đồng bộ." });
    expect(rows[1]?.label).toBe("…000009");
  });

  it("falls back to the parked sales of the outbox when the queue cannot be read", () => {
    const rows = parkedSales(undefined, [parked(1), testOutboxEntry(2)]);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ id: "sx-1", label: "OFF-001", cash: "100000" });
    expect(rows[0]?.body?.lines).toEqual(parked(1).sale.lines);
  });

  it("leaves an unreadable payload without a body, so it cannot be posted again", () => {
    const mine = parked(1);
    const rows = parkedSales([{ ...testSyncException(mine), payload: { nope: true } }], [mine]);
    expect(rows[0]?.body).toBeNull();
    expect(rows[0]?.cash).toBeNull();
    expect(rows[0]?.createdAt).toBe(mine.sale.created_at);
    expect(rows[0]?.lines).toEqual(mine.lines);
  });
});

describe("offline messages", () => {
  it("announces a finished sync with the parked count only when there is one", () => {
    expect(syncDoneMessage(3, 0)).toBe("Đồng bộ xong: 3 giao dịch ghi sổ. Mỗi giao dịch chỉ ghi một lần.");
    expect(syncDoneMessage(2, 1)).toBe("Đồng bộ xong: 2 giao dịch ghi sổ, 1 vào hàng đợi ngoại lệ. Mỗi giao dịch chỉ ghi một lần.");
  });

  it("confirms a sale saved on the device and prefixes refusals with “Chưa lưu.”", () => {
    expect(savedOfflineMessage("OFF-004")).toBe("Đã lưu OFF-004 trên máy. Sẽ ghi sổ một lần khi có mạng.");
    render(<p>{notSavedMessage("Ngoại tuyến chỉ cho bán thu đủ tiền mặt.")}</p>);
    expect(screen.getByText("Chưa lưu.").tagName).toBe("B");
    expect(screen.getByText(/Ngoại tuyến chỉ cho bán thu đủ tiền mặt\./)).toBeInTheDocument();
  });

  it("translates the server's reasons, known codes first", () => {
    expect(syncErrorText("offline_amount_exceeded", "x")).toBe("Vượt hạn mức bán ngoại tuyến.");
    expect(syncErrorText("sync_exception_closed", null)).toBe("Giao dịch đã được xử lý.");
    expect(syncErrorText("some_unknown_code", "raw detail")).toBeTruthy();
  });
});
