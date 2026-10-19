import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { usePosCart, type PosProduct } from "../hooks/use-pos-cart";
import { listCheckoutIntents } from "../offline/checkout-intents";
import { connectivity, useConnectivity } from "../offline/connectivity";
import { OFFLINE_MESSAGES } from "../offline/offline-sale-service";
import { listOutbox } from "../offline/outbox-repository";
import { resetOfflineDb } from "../offline/testing/fake-offline-db";
import { OFFLINE_METHODS, OFFLINE_TENANT_ID, bootstrapHandlers, syncExceptionsHandler, testBundle, testPosSale } from "../offline/testing/handlers";
import { useOfflineSync } from "../offline/use-offline-sync";
import { OFFLINE_HOLD_MESSAGE, PosCartContainer, UNCERTAIN_CHECKOUT_MESSAGE } from "./pos-cart-container";

const KEO: PosProduct = { skuId: "sku-keo", skuLabel: "KEO-DUA · Kẹo dừa", unitId: "unit-goi", autoColor: null };
const BUNDLE = testBundle();
const BASE_LIST = { kind: "base_list", price_list_name: "Bảng giá chung", color_id: null };

/** The POS screen's cart column with the page's connectivity and background sync. */
function Screen() {
  const cart = usePosCart();
  const view = useConnectivity();
  useOfflineSync(OFFLINE_TENANT_ID);
  return (
    <>
      <button type="button" onClick={() => cart.add(KEO)}>
        Thêm Kẹo dừa
      </button>
      <button type="button" onClick={() => cart.setCustomer({ id: "party-an", name: "Chị An" })}>
        Chọn Chị An
      </button>
      <PosCartContainer cart={cart} catalog={null} methods={OFFLINE_METHODS} onFocusScan={() => undefined} offline={{ offline: view.state !== "online", bundle: BUNDLE }} />
    </>
  );
}

/** Server pricing of one Kẹo dừa at 50 000 đ. */
function pricingHandlers() {
  return [
    http.post(apiUrl("/pricing/quote"), () => HttpResponse.json({ data: [{ amount: "50000", unit_id: "unit-goi", price_source: BASE_LIST }] })),
    http.post(apiUrl("/pricing/calculate"), () =>
      HttpResponse.json({
        lines: [
          {
            draft_index: 0,
            line_kind: "item",
            sku_id: "sku-keo",
            color_id: null,
            unit_id: "unit-goi",
            qty: "1",
            unit_price: "50000",
            gross_amount: "50000",
            discount_amount: "0",
            line_amount: "50000",
            below_min_price: false,
            price_source: BASE_LIST,
          },
        ],
        goods_amount: "50000",
        discount_amount: "0",
        tax_amount: "0",
        total_amount: "50000",
      }),
    ),
  ];
}

interface CheckoutCall {
  key: string | null;
  body: unknown;
}

/** POST /pos/checkout whose first answer is lost on the way; later calls post HD-0042. */
function lostAnswerCheckout() {
  const calls: CheckoutCall[] = [];
  const handler = http.post(apiUrl("/pos/checkout"), async ({ request }) => {
    calls.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
    return calls.length === 1 ? HttpResponse.error() : HttpResponse.json(testPosSale("HD-0042"), { status: 201 });
  });
  return { calls, handler };
}

async function fillCart() {
  await userEvent.click(screen.getByRole("button", { name: "Thêm Kẹo dừa" }));
  const chips = await screen.findByRole("group", { name: "Tiền khách đưa nhanh" });
  // The first chip is the exact total.
  await userEvent.click(within(chips).getAllByRole("button")[0] as HTMLElement);
}

beforeEach(resetOfflineDb);
afterEach(() => connectivity.reset());

describe("PosCartContainer", () => {
  it("never sells offline a cart whose online checkout got no answer, and settles it with the same key once back online", async () => {
    const checkout = lostAnswerCheckout();
    server.use(...bootstrapHandlers(), ...pricingHandlers(), checkout.handler, syncExceptionsHandler(() => []));
    renderWithProviders(<Screen />);
    await fillCart();

    await userEvent.click(screen.getByRole("button", { name: "Thanh toán · F9" }));
    expect(await screen.findByText(UNCERTAIN_CHECKOUT_MESSAGE)).toBeInTheDocument();
    // The lost answer made the page check the connection; keep it offline while the cashier retries.
    await waitFor(() => expect(connectivity.getState()).not.toBe("online"));
    connectivity.simulate(true);
    const [intent] = await listCheckoutIntents(OFFLINE_TENANT_ID);
    expect(intent?.idempotency_key).toBe(checkout.calls[0]?.key);

    await userEvent.click(screen.getByRole("button", { name: "Thanh toán · F9" }));
    expect(await screen.findByText(OFFLINE_MESSAGES.uncertain, { exact: false })).toBeInTheDocument();
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
    expect(checkout.calls).toHaveLength(1);

    connectivity.reset();
    expect(await screen.findByText(/HD-0042/)).toBeInTheDocument();
    expect(checkout.calls).toHaveLength(2);
    expect(checkout.calls[1]).toEqual(checkout.calls[0]);
    await waitFor(() => expect(screen.queryByText(UNCERTAIN_CHECKOUT_MESSAGE)).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Thanh toán · F9" })).toBeDisabled();
    expect(await listCheckoutIntents(OFFLINE_TENANT_ID)).toEqual([]);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("warns offline that a chosen customer cannot be sold to at their own prices", async () => {
    server.use(...bootstrapHandlers(), ...pricingHandlers(), syncExceptionsHandler(() => []));
    connectivity.simulate(true);
    renderWithProviders(<Screen />);
    await userEvent.click(screen.getByRole("button", { name: "Thêm Kẹo dừa" }));
    expect(screen.queryByText(OFFLINE_MESSAGES.customer)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Chọn Chị An" }));
    expect((await screen.findByText(OFFLINE_MESSAGES.customer)).closest('[aria-live="polite"]')).not.toBeNull();
  });

  it("names the cart table and each line's controls after the product", async () => {
    server.use(
      ...bootstrapHandlers(),
      ...pricingHandlers(),
      syncExceptionsHandler(() => []),
      http.get(apiUrl("/stock/sku-keo"), () => HttpResponse.json({ sku_id: "sku-keo", unit_name: "Gói", available: "40", colors: [{ color_id: "c-do", sellable: "40" }] })),
    );
    renderWithProviders(<Screen />);
    await userEvent.click(screen.getByRole("button", { name: "Thêm Kẹo dừa" }));

    const table = screen.getByRole("table", { name: "Giỏ hàng" });
    expect(within(table).getAllByRole("columnheader").at(-1)).toHaveAccessibleName("Thao tác");
    expect(await within(table).findByRole("radiogroup", { name: "Màu khách lấy – Kẹo dừa" })).toBeInTheDocument();
    // The only color in stock is picked, and the row's controls name it: one product split by
    // color fills several rows.
    expect(await within(table).findByRole("textbox", { name: "Số lượng – Kẹo dừa Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Đơn vị – Kẹo dừa Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa Kẹo dừa Đỏ" })).toHaveTextContent("Xóa");
    expect(within(table).getByRole("button", { name: "Thêm màu khác – Kẹo dừa" })).toHaveTextContent("+ Thêm màu khác");
  });

  it("announces the amount due once the cart's total settles", async () => {
    server.use(...bootstrapHandlers(), ...pricingHandlers(), syncExceptionsHandler(() => []));
    renderWithProviders(<Screen />);

    const due = screen.getByText("Khách cần trả").parentElement?.querySelector('[role="status"]');
    expect(due).toBeInstanceOf(HTMLElement);
    expect(due).toHaveClass("sr-only");
    expect(due).toBeEmptyDOMElement();

    await userEvent.click(screen.getByRole("button", { name: "Thêm Kẹo dừa" }));
    await waitFor(() => expect(due).toHaveTextContent(/^Khách cần trả 50\.000\s₫$/));
  });

  it("states why the cart cannot be saved offline next to the disabled button", async () => {
    server.use(...bootstrapHandlers(), ...pricingHandlers(), syncExceptionsHandler(() => []));
    connectivity.simulate(true);
    renderWithProviders(<Screen />);
    await userEvent.click(screen.getByRole("button", { name: "Thêm Kẹo dừa" }));

    const hold = screen.getByRole("button", { name: "Lưu giỏ · F4" });
    expect(hold).toBeDisabled();
    expect(hold).toHaveAccessibleDescription(OFFLINE_HOLD_MESSAGE);
    expect(screen.getByText(OFFLINE_HOLD_MESSAGE)).toBeVisible();
    expect(hold).toHaveAttribute("aria-keyshortcuts", "F4");
    expect(screen.getByRole("button", { name: "Thanh toán · F9" })).toHaveAttribute("aria-keyshortcuts", "F9");
  });
});
