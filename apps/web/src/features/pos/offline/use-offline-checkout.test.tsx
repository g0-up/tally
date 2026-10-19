import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toQty } from "../../../shared/domain/qty";
import { renderWithProviders } from "../../../test/render";
import { newItemLine } from "../../sales/components/line-items/draft";
import type { OfflineBundle, OutboxEntry } from "./offline-db";
import { subscribeOfflineEvents, type OfflineEvent } from "./offline-events";
import { addToOutbox, listOutbox } from "./outbox-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { OFFLINE_TENANT_ID, newOutboxEntry, testBundle } from "./testing/handlers";
import { useOfflineCheckout } from "./use-offline-checkout";

const NOW = new Date("2026-10-18T09:00:00.000Z");

function Checkout({ bundle, cash, credit = false, after }: { bundle: OfflineBundle | null; cash: string; credit?: boolean; after: (e: OutboxEntry) => void }) {
  const checkout = useOfflineCheckout(bundle, after);
  const lines = [newItemLine({ skuId: "sku-keo", skuLabel: "KEO-DUA · Kẹo dừa", unitId: "unit-goi", qty: toQty(2) })];
  return (
    <button type="button" onClick={() => void checkout.run({ lines, customerId: null, cash, bank: "", credit, idOf: () => null, now: NOW })}>
      Thanh toán
    </button>
  );
}

beforeEach(resetOfflineDb);

describe("useOfflineCheckout", () => {
  it("saves the sale on the device, confirms with its OFF number, tells the other tabs and clears the cart", async () => {
    const after = vi.fn();
    const events: OfflineEvent[] = [];
    const stop = subscribeOfflineEvents((e) => events.push(e));
    renderWithProviders(<Checkout bundle={testBundle()} cash="100000" after={after} />);
    await userEvent.click(screen.getByRole("button", { name: "Thanh toán" }));

    expect(await screen.findByText("Đã lưu OFF-001 trên máy. Sẽ ghi sổ một lần khi có mạng.")).toBeInTheDocument();
    await waitFor(() => expect(after).toHaveBeenCalledWith(expect.objectContaining({ label: "OFF-001" })));
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
    expect(events).toEqual([{ type: "changed", tenantId: OFFLINE_TENANT_ID }]);
    stop();
  });

  it("refuses credit with “Chưa lưu.” and keeps the cart", async () => {
    const after = vi.fn();
    renderWithProviders(<Checkout bundle={testBundle()} cash="100000" credit after={after} />);
    await userEvent.click(screen.getByRole("button", { name: "Thanh toán" }));

    expect(await screen.findByText(/Ngoại tuyến chỉ cho bán thu đủ tiền mặt\./)).toBeInTheDocument();
    expect(screen.getByText("Chưa lưu.")).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("counts the sales already on the device against the cap", async () => {
    for (let n = 1; n <= 19; n++) await addToOutbox(newOutboxEntry(n));
    renderWithProviders(<Checkout bundle={testBundle()} cash="100000" after={() => undefined} />);
    await userEvent.click(screen.getByRole("button", { name: "Thanh toán" }));
    // 19 × 100 000 đ waiting + 100 000 đ = the 2 000 000 đ cap: still allowed.
    expect(await screen.findByText(/Đã lưu OFF-020 trên máy/)).toBeInTheDocument();
  });

  it("refuses past the cap and stores nothing", async () => {
    const after = vi.fn();
    for (let n = 1; n <= 20; n++) await addToOutbox(newOutboxEntry(n));
    renderWithProviders(<Checkout bundle={testBundle()} cash="100000" after={after} />);
    await userEvent.click(screen.getByRole("button", { name: "Thanh toán" }));
    expect(await screen.findByText(/Vượt hạn mức bán ngoại tuyến\. Kết nối mạng để tiếp tục\./)).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(20);
  });
});
