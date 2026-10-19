import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testSku } from "../../procurement/testing/fixtures";
import { KIT_UNITS, testCostView, testKit } from "../testing/fixtures";

const draftKit = testKit();
const sellingKit = testKit({
  product_id: "kit-2",
  name: "Giỏ Phú Quý",
  sku_id: "sku-kit2",
  sku_code: "GQPQ",
  status: "active",
  active_version: 1,
  version: 5,
  latest: { ...draftKit.latest, status: "active" },
});
const listItem = (k: typeof draftKit) => ({
  product_id: k.product_id,
  type: k.type,
  name: k.name,
  status: k.status,
  sku_id: k.sku_id,
  sku_code: k.sku_code,
  latest_version: k.latest.version_no,
  latest_status: k.latest.status,
  active_version: k.active_version,
  version: k.version,
});

/** A kit selling v1 of two products while v2, reduced to one product, waits as a draft. */
const waitingKit = testKit({
  product_id: "kit-3",
  name: "Giỏ Như Ý",
  sku_id: "sku-kit3",
  sku_code: "GQNY",
  status: "active",
  active_version: 1,
  version: 7,
  latest: { ...draftKit.latest, id: "rcp-3b", version_no: 2 },
});
const sellingRecipe = {
  ...draftKit.latest,
  id: "rcp-3a",
  status: "active",
  items: [
    ...draftKit.latest.items,
    { id: "ri-9", line_no: 2, kind: "goods", sku_id: "sku-2", unit_id: draftKit.latest.items[0]?.unit_id, qty: "1", base_qty: "1", flavors: [] },
  ],
};

/** A draft kit and a selling one, their economics, and a recorder of publish calls (If-Match). */
function recipesServer(keys: (string | null)[] = []) {
  const published: (string | null)[] = [];
  server.use(
    http.get(apiUrl("/kits"), () => HttpResponse.json({ data: [listItem(draftKit), listItem(sellingKit)], next_cursor: null })),
    http.get(apiUrl("/kits/:id"), ({ params }) => HttpResponse.json(params.id === "kit-1" ? draftKit : sellingKit)),
    http.get(apiUrl("/kits/:id/preview"), () => HttpResponse.json(testCostView())),
    http.post(/\/kits\/kit-1:publish$/, ({ request }) => {
      published.push(request.headers.get("If-Match"));
      keys.push(request.headers.get("Idempotency-Key"));
      return HttpResponse.json({ ...draftKit, status: "active", active_version: 1, version: 2 });
    }),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku(), testSku({ id: "sku-kit", code: "GQAK", display_name: "Giỏ An Khang", retail_price: toMoney(500000), wholesale_price: toMoney(450000) })], next_cursor: null })),
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: KIT_UNITS, next_cursor: null })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
  );
  return published;
}

const cardOf = (name: string, table = screen.getByRole("table", { name: `Thành phần ${name}` })) => table.closest(".card") as HTMLElement;

describe("Giỏ quà & combo page", () => {
  it("narrows to drafts and publishes one against its current version", async () => {
    const published = recipesServer();
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    await screen.findByRole("table", { name: "Thành phần Giỏ Phú Quý" });
    expect(within(cardOf("Giỏ Phú Quý")).getByRole("button", { name: "Tạo lệnh đóng" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Nháp (1)" }));
    expect(screen.getByRole("button", { name: "Nháp (1)" })).toHaveAttribute("aria-pressed", "true");
    await waitFor(() => expect(screen.queryByRole("table", { name: "Thành phần Giỏ Phú Quý" })).not.toBeInTheDocument());

    const draft = cardOf("Giỏ An Khang");
    expect(within(draft).getByText("chưa bán được cho tới khi phát hành")).toBeInTheDocument();
    await user.click(within(draft).getByRole("button", { name: "Phát hành" }));

    await waitFor(() => expect(published).toEqual([etagOf(1)]));
    expect(await screen.findByText("Đã phát hành. Giỏ An Khang bán được từ bây giờ.")).toBeInTheDocument();
  });

  it("resends a publish that got no answer under the same key", async () => {
    const keys: (string | null)[] = [];
    const published = recipesServer(keys);
    let lost = false;
    server.use(
      http.post(/\/kits\/kit-1:publish$/, ({ request }) => {
        if (lost) return undefined;
        lost = true;
        published.push(request.headers.get("If-Match"));
        keys.push(request.headers.get("Idempotency-Key"));
        return HttpResponse.error();
      }),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    await screen.findByRole("table", { name: "Thành phần Giỏ An Khang" });
    const publish = within(cardOf("Giỏ An Khang")).getByRole("button", { name: "Phát hành" });
    await user.click(publish);
    await waitFor(() => expect(keys).toHaveLength(1));
    await waitFor(() => expect(publish).toBeEnabled());
    await user.click(publish);

    expect(await screen.findByText("Đã phát hành. Giỏ An Khang bán được từ bây giờ.")).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
    expect(published).toEqual([etagOf(1), etagOf(1)]);
  });

  it("publishes another kit while one publish has no answer, under a key of that kit", async () => {
    recipesServer();
    const otherDraft = testKit({ product_id: "kit-4", name: "Giỏ Bình An", sku_id: "sku-kit4", sku_code: "GQBA", latest: { ...draftKit.latest, id: "rcp-4" } });
    // The server binds a key to the path it first stored it with and refuses it on another one.
    const bound = new Map<string, string>();
    const sent: { path: string; key: string | null }[] = [];
    const stored = (path: string, key: string | null) => {
      sent.push({ path, key });
      if (key === null) return apiError(428, "idempotency_key_required");
      const first = bound.get(key);
      if (first !== undefined && first !== path) return apiError(409, "idempotency_key_reused");
      bound.set(key, path);
      return null;
    };
    server.use(
      http.get(apiUrl("/kits"), () => HttpResponse.json({ data: [listItem(draftKit), listItem(otherDraft)], next_cursor: null })),
      http.get(apiUrl("/kits/:id"), ({ params }) => HttpResponse.json(params.id === "kit-4" ? otherDraft : draftKit)),
      http.post(/\/kits\/kit-1:publish$/, ({ request }) => stored("kit-1", request.headers.get("Idempotency-Key")) ?? HttpResponse.error()),
      http.post(
        /\/kits\/kit-4:publish$/,
        ({ request }) => stored("kit-4", request.headers.get("Idempotency-Key")) ?? HttpResponse.json({ ...otherDraft, status: "active", active_version: 1, version: 2 }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    await screen.findByRole("table", { name: "Thành phần Giỏ Bình An" });
    const first = within(cardOf("Giỏ An Khang")).getByRole("button", { name: "Phát hành" });
    await user.click(first);
    await waitFor(() => expect(sent).toHaveLength(1));
    await waitFor(() => expect(first).toBeEnabled());
    await user.click(within(cardOf("Giỏ Bình An")).getByRole("button", { name: "Phát hành" }));

    expect(await screen.findByText("Đã phát hành. Giỏ Bình An bán được từ bây giờ.")).toBeInTheDocument();
    expect(sent.map((s) => s.path)).toEqual(["kit-1", "kit-4"]);
    expect(sent[1]?.key).not.toBe(sent[0]?.key);
  });

  it("previews a draft as the customer sees it", async () => {
    recipesServer();
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    await screen.findByRole("table", { name: "Thành phần Giỏ An Khang" });
    await user.click(within(cardOf("Giỏ An Khang")).getByRole("button", { name: "Xem trước" }));

    const drawer = await screen.findByRole("dialog", { name: "Xem trước · Giỏ An Khang" });
    expect(within(drawer).getByText("Bản nháp")).toBeInTheDocument();
    expect(await within(drawer).findByText("Giá doanh nghiệp / sỉ: 450.000 ₫")).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Sửa nháp" })).toBeInTheDocument();
  });

  it("deletes a draft against its version", async () => {
    recipesServer();
    const deleted: (string | null)[] = [];
    server.use(
      http.delete(apiUrl("/kits/kit-1"), ({ request }) => {
        deleted.push(request.headers.get("If-Match"));
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    await screen.findByRole("table", { name: "Thành phần Giỏ An Khang" });
    await user.click(within(cardOf("Giỏ An Khang")).getByRole("button", { name: "Xóa nháp" }));
    const dialog = await screen.findByRole("dialog", { name: "Xóa nháp Giỏ An Khang?" });
    await user.click(within(dialog).getByRole("button", { name: "Xóa nháp" }));

    await waitFor(() => expect(deleted).toEqual([etagOf(1)]));
    expect(await screen.findByText("Đã xóa nháp.")).toBeInTheDocument();
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
  });

  it("keeps showing the selling version of a kit while its next version waits as a draft", async () => {
    const previewed: unknown[] = [];
    const published: (string | null)[] = [];
    server.use(
      http.get(apiUrl("/kits"), () => HttpResponse.json({ data: [listItem(waitingKit)], next_cursor: null })),
      http.get(apiUrl("/kits/:id"), () => HttpResponse.json(waitingKit)),
      http.get(apiUrl("/kits/:id/preview"), () => HttpResponse.json(testCostView())),
      http.get(apiUrl("/kits/kit-3/recipes/1"), () => HttpResponse.json(sellingRecipe)),
      http.post(/\/kits:preview$/, async ({ request }) => {
        previewed.push(await request.json());
        return HttpResponse.json(testCostView());
      }),
      http.post(/\/kits\/kit-3:publish$/, ({ request }) => {
        published.push(request.headers.get("If-Match"));
        return HttpResponse.json({ ...waitingKit, active_version: 2, latest: { ...waitingKit.latest, status: "active" }, version: 8 });
      }),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
      http.get(apiUrl("/units"), () => HttpResponse.json({ data: KIT_UNITS, next_cursor: null })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/recipes");

    const card = cardOf("Giỏ Như Ý", await screen.findByRole("table", { name: "Thành phần Giỏ Như Ý" }));
    expect(within(card).getByText("Giỏ Như Ý · v1")).toBeInTheDocument();
    // The components shown are those of the selling version, priced from its recipe.
    expect(previewed).toEqual([{ items: sellingRecipe.items.map(({ kind, sku_id, unit_id, qty }) => ({ kind, sku_id, unit_id, qty })), packing_cost: "20000" }]);
    expect(within(card).getByText(/Đang bán v1 · có nháp v2/)).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Sửa nháp" })).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Tạo lệnh đóng" })).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Tạo phiên bản mới" })).not.toBeInTheDocument();

    await user.click(within(card).getByRole("button", { name: "Phát hành v2" }));
    await waitFor(() => expect(published).toEqual([etagOf(7)]));
    expect(await screen.findByText((_, el) => el?.classList.contains("toast") === true && el.textContent === "Đã phát hành v2. Giỏ Như Ý dùng phiên bản này từ bây giờ.")).toBeInTheDocument();
  });
});
