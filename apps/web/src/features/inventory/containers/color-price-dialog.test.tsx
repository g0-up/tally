import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { COLOR_RED, testColorStock } from "../testing/fixtures";
import { ColorPriceDialog } from "./color-price-dialog";

const sku = testSku({ wholesale_price: toMoney(12000), avg_cost: toMoney(9000) });
const red = testColorStock();

/** Records every `POST /skus/sku-1/sale-price` body. */
function salePrices() {
  const bodies: unknown[] = [];
  server.use(
    http.post(/\/skus\/sku-1\/sale-price$/, async ({ request }) => {
      bodies.push(await request.json());
      return HttpResponse.json({});
    }),
  );
  return bodies;
}

describe("ColorPriceDialog", () => {
  it("sets a color's own retail price on top of the product price", async () => {
    const bodies = salePrices();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={undefined} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    expect(within(dialog).getByText(/^Màu này đang dùng giá chung/)).toBeInTheDocument();
    expect(within(dialog).getByText("10 hộp")).toBeInTheDocument();
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    await user.clear(retail);
    await user.type(retail, "18000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    // The wholesale price was left empty, so the color keeps following the product's.
    expect(bodies).toEqual([{ kind: "retail", color_id: COLOR_RED, price: "18000", source: "inventory" }]);
    expect(await screen.findByText("Đã lưu giá riêng Đỏ.")).toBeInTheDocument();
  });

  it("refuses a wholesale price above the retail price", async () => {
    const bodies = salePrices();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={undefined} onClose={() => {}} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    await user.clear(retail);
    await user.type(retail, "10000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));
    expect(await screen.findByText(/Giá sỉ không được cao hơn giá bán lẻ\./)).toBeInTheDocument();
    expect(bodies).toEqual([]);
  });

  it("drops a color's own prices back to the product price", async () => {
    const bodies = salePrices();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={{ retail: toMoney(18000), wholesale: null }} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    expect(within(dialog).getByText("Màu này đang dùng giá riêng.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("checkbox", { name: /^Bỏ giá riêng, dùng giá chung của sản phẩm/ }));
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    expect(retail).toHaveAttribute("readonly");
    expect(retail).toBeEnabled();
    retail.focus();
    expect(retail).toHaveFocus();
    expect(within(dialog).getByText(/^Giá chung .* sẽ áp dụng cho màu này\.$/)).toBeInTheDocument();
    expect(retail).toHaveAccessibleDescription(/^Giá chung .* sẽ áp dụng cho màu này\.$/);
    expect(within(dialog).getByRole("textbox", { name: "Giá sỉ / hộp" })).toHaveAccessibleDescription(/^Giá chung .* sẽ áp dụng cho màu này\.$/);
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(bodies).toEqual([{ kind: "retail", color_id: COLOR_RED, price: null, source: "inventory" }]);
    expect(await screen.findByText("Đã đưa Đỏ về giá chung.")).toBeInTheDocument();
  });

  it("lowers the wholesale price before a retail price that goes below it", async () => {
    const bodies = salePrices();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={{ retail: toMoney(18000), wholesale: toMoney(15000) }} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    const wholesale = within(dialog).getByRole("textbox", { name: "Giá sỉ / hộp" });
    await user.clear(retail);
    await user.type(retail, "14000");
    await user.clear(wholesale);
    await user.type(wholesale, "13000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(bodies).toEqual([
      { kind: "wholesale", color_id: COLOR_RED, price: "13000", source: "inventory" },
      { kind: "retail", color_id: COLOR_RED, price: "14000", source: "inventory" },
    ]);
  });

  it("keeps the price that went through and sends the one edited after a failed write", async () => {
    const bodies: { kind: string; price: string | null }[] = [];
    server.use(
      http.post(/\/skus\/sku-1\/sale-price$/, async ({ request }) => {
        const body = (await request.json()) as { kind: string; price: string | null };
        bodies.push(body);
        return body.kind === "wholesale" && bodies.length === 2 ? HttpResponse.error() : HttpResponse.json({});
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={undefined} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    const wholesale = within(dialog).getByRole("textbox", { name: "Giá sỉ / hộp" });
    await user.clear(retail);
    await user.type(retail, "18000");
    await user.type(wholesale, "16000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    expect(await screen.findByText(/Đã lưu một phần giá Đỏ nhưng chưa lưu giá sỉ/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    await user.clear(wholesale);
    await user.type(wholesale, "15000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(bodies.map((b) => [b.kind, b.price])).toEqual([
      ["retail", "18000"],
      ["wholesale", "16000"],
      ["wholesale", "15000"],
    ]);
  });

  it("resends a price write that got no answer under the same key", async () => {
    const keys: Array<string | null> = [];
    server.use(
      http.post(/\/skus\/sku-1\/sale-price$/, ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json({}, { headers: { "Idempotent-Replay": "true" } });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ColorPriceDialog sku={sku} color={red} own={undefined} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Giá bán Đỏ · Bánh quy bơ" });
    const retail = within(dialog).getByRole("textbox", { name: "Giá bán lẻ / hộp" });
    await user.clear(retail);
    await user.type(retail, "18000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    expect(onClose).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá màu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(new RegExp(`:sale-price:sku-1:retail:${COLOR_RED}:inventory:18000:1$`));
    expect(keys[1]).toBe(keys[0]);
  });
});
