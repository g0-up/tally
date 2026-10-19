import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import type { ColorKey } from "../../../../shared/domain/color";
import type { Money } from "../../../../shared/domain/money";
import { toQty } from "../../../../shared/domain/qty";
import { apiUrl } from "../../../../test/msw/handlers";
import { server } from "../../../../test/msw/server";
import { renderWithProviders } from "../../../../test/render";
import type { ColorMaps } from "../../api/queries";
import type { MixGroup } from "../../schemas";
import { newItemLine, newMixLine } from "./draft";
import { LineItemsEditor } from "./line-items-editor";
import type { LinePricing } from "./use-line-pricing";

const NO_PRICES: LinePricing = {
  listPrice: () => null,
  flavorPrice: () => null,
  lineAmount: () => null,
  grossAmount: () => null,
  belowMin: () => false,
  totals: undefined,
  pending: false,
};

const COLORS: ColorMaps = {
  codeOf: (id) => (id === "color-do" ? "do" : id === "color-vang" ? "vang" : "none"),
  idOf: (c: ColorKey | null) => (c === "do" ? "color-do" : c === "vang" ? "color-vang" : null),
};

const GROUP: MixGroup = {
  id: "mix-1",
  name: "Kẹo Tết",
  pack_unit_id: "unit-thung",
  units_per_pack: 24,
  part_unit_id: "unit-day",
  units_per_part: 6,
  piece_unit_id: "unit-goi",
  status: "active",
  items: [
    { sku_id: "sku-dau", sku_code: "KD", sku_name: "Kẹo dâu", flavor_label: "Dâu", active: true },
    { sku_id: "sku-cam", sku_code: "KC", sku_name: "Kẹo cam", flavor_label: "Cam", active: true },
  ],
};

const UNIT_NAMES: Record<string, string> = { "unit-thung": "thùng", "unit-day": "dây", "unit-goi": "gói" };

function withCatalog() {
  server.use(
    http.get(apiUrl("/skus/:id"), ({ params }) =>
      HttpResponse.json({
        id: params.id,
        code: "SP001",
        display_name: "Bánh quy bơ",
        variant_label: "",
        min_price: null,
        units: [{ unit_id: "unit-hop", unit_name: "Hộp", factor: "1", is_base: true, active: true, is_default_sale: true }],
      }),
    ),
    http.get(apiUrl("/stock/:id"), ({ params }) =>
      HttpResponse.json({
        sku_id: params.id,
        unit_name: "Hộp",
        available: "9",
        colors: [
          { color_id: "color-do", sellable: "4" },
          { color_id: "color-vang", sellable: "5" },
        ],
      }),
    ),
  );
}

interface EditorOptions {
  packs?: string;
  parts?: Record<string, string>;
  pricing?: LinePricing;
}

function renderEditor(preset: "sell" | "quote", over: EditorOptions = {}) {
  withCatalog();
  const lines = [
    newItemLine({ skuId: "sku-1", skuLabel: "SP001 · Bánh quy bơ", unitId: "unit-hop", qty: toQty("2"), color: "do" }),
    newItemLine({ skuId: "sku-1", skuLabel: "SP001 · Bánh quy bơ", unitId: "unit-hop", qty: toQty("1"), color: "vang" }),
    newItemLine(),
    {
      ...newMixLine(GROUP),
      packs: toQty(over.packs ?? "1"),
      parts: Object.fromEntries(Object.entries(over.parts ?? { "sku-dau": "3", "sku-cam": "2" }).map(([k, v]) => [k, toQty(v)])),
    },
  ];
  renderWithProviders(
    <LineItemsEditor
      preset={preset}
      lines={lines}
      onChange={vi.fn()}
      pricing={over.pricing ?? NO_PRICES}
      groups={[GROUP]}
      colors={COLORS}
      unitName={(id) => UNIT_NAMES[id] ?? ""}
    />,
  );
}

/** Every accessible name of the form controls in the table, in order. */
function controlNames(table: HTMLElement): string[] {
  return (["textbox", "combobox", "button", "radiogroup"] as const)
    .flatMap((role) => within(table).queryAllByRole(role))
    .map((el) => el.getAttribute("aria-label") ?? "")
    .filter((name) => name !== "");
}

describe("LineItemsEditor", () => {
  it("names the table and its action column for screen readers", async () => {
    renderEditor("sell");
    const table = await screen.findByRole("table", { name: "Dòng hàng" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.at(-1)).toHaveAccessibleName("Thao tác");
  });

  it("names each control of a product line after its product and color, or its row number", async () => {
    renderEditor("sell");
    const table = await screen.findByRole("table", { name: "Dòng hàng" });
    expect(await within(table).findByRole("radiogroup", { name: "Màu khách lấy – Bánh quy bơ Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("radiogroup", { name: "Màu khách lấy – Bánh quy bơ Vàng" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – Bánh quy bơ Đỏ" })).toHaveValue("2");
    expect(within(table).getByRole("textbox", { name: "Số lượng – Bánh quy bơ Vàng" })).toHaveValue("1");
    expect(within(table).getByRole("textbox", { name: "Giá – Bánh quy bơ Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Chiết khấu % – Bánh quy bơ Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Đơn vị tính – Bánh quy bơ Đỏ" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa – Bánh quy bơ Vàng" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Tên sản phẩm – dòng 1" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – dòng 3" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa – dòng 3" })).toBeInTheDocument();
  });

  it("names the controls of a mix pack after its group and row, and never repeats a name", async () => {
    renderEditor("quote");
    const table = await screen.findByRole("table", { name: "Dòng hàng" });
    await within(table).findByRole("radiogroup", { name: "Màu sắc – Bánh quy bơ Đỏ" });
    expect(within(table).getByRole("combobox", { name: "Chọn nhóm mix vị – Kẹo Tết, dòng 4" })).toHaveValue("mix-1");
    expect(within(table).getByRole("textbox", { name: "Số thùng – Kẹo Tết, dòng 4" })).toHaveValue("1");
    expect(within(table).getByRole("textbox", { name: "Số dây vị Dâu – Kẹo Tết, dòng 4" })).toHaveValue("3");
    expect(within(table).getByRole("textbox", { name: "Quy cách – Kẹo Tết, dòng 4" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Quy cách – dòng 3" })).toBeInTheDocument();
    const names = controlNames(table);
    expect(names.length).toBeGreaterThan(20);
    expect(new Set(names).size).toBe(names.length);
  });

  it("announces the mix count and an over-filled pack through a live region that is always there", async () => {
    renderEditor("sell", { packs: "0" });
    const table = await screen.findByRole("table", { name: "Dòng hàng" });
    const status = await within(table).findByText("Nhập số thùng.");
    const region = status.closest("[aria-live]");
    expect(region).toHaveAttribute("aria-live", "polite");
    expect(region?.parentElement?.closest("[aria-live], [role=alert], [role=status]")).toBeNull();
  });

  describe("pack price", () => {
    const FLAVOR_PRICES: Readonly<Record<string, string>> = { "sku-dau": "10000", "sku-cam": "12000" };
    const priced = (gross: string | null, prices = FLAVOR_PRICES): LinePricing => ({
      ...NO_PRICES,
      flavorPrice: (_key, skuId) => (prices[skuId] ?? null) as Money | null,
      grossAmount: () => gross as Money | null,
    });
    const mixRow = async () => {
      const table = await screen.findByRole("table", { name: "Dòng hàng" });
      const row = (await within(table).findByRole("combobox", { name: "Chọn nhóm mix vị – Kẹo Tết, dòng 4" })).closest("tr");
      if (!row) throw new Error("no mix row");
      return row;
    };

    it("prices a short pack by the flavors chosen over its packs, as the server stores and prints it", async () => {
      // 2 thùng of 4 dây each, 3 + 2 dây chosen: 30 gói at 10.000 = 300.000 over 2 thùng.
      renderEditor("sell", { packs: "2", pricing: priced(null, { "sku-dau": "10000", "sku-cam": "10000" }) });
      expect(await mixRow()).toHaveTextContent("150.000 ₫/ thùng");
    });

    it("weighs each flavor by its own price and parts", async () => {
      // 18 gói at 10.000 and 12 gói at 12.000 = 324.000 over 2 thùng.
      renderEditor("sell", { packs: "2", pricing: priced(null) });
      expect(await mixRow()).toHaveTextContent("162.000 ₫/ thùng");
    });

    it("takes the calculated gross once the server has priced the document", async () => {
      // Document rounding can leave the gross a few dong off price × quantity; the server divides that.
      renderEditor("sell", { packs: "2", pricing: priced("324004") });
      expect(await mixRow()).toHaveTextContent("162.002 ₫/ thùng");
    });

    it("shows the average flavor price of a full pack before any flavor is chosen", async () => {
      // (10.000 + 12.000) / 2 × 24 gói.
      renderEditor("sell", { packs: "2", parts: { "sku-dau": "0", "sku-cam": "0" }, pricing: priced(null) });
      expect(await mixRow()).toHaveTextContent("264.000 ₫/ thùng");
    });

    it("keeps the retail column of a quote at the price of a full pack", async () => {
      renderEditor("quote", { packs: "2", pricing: priced(null) });
      const cells = within(await mixRow()).getAllByRole("cell");
      expect(cells.at(4)).toHaveTextContent("162.000 ₫/thùng");
      expect(cells.at(-2)).toHaveTextContent("264.000 ₫/thùng");
    });
  });
});
