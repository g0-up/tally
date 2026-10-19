import { randomUUID } from "node:crypto";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { vnd } from "../../src/shared/lib/format";
import { apiGet, apiPost } from "../support/real-api";
import { activeCustomers, errToast, escapeRe, okToast, pickOption } from "../support/sales-data";
import { addStockApi, apiPostRaw, finishJourney, onHandByColor, requireOwner, uniqueSuffix, watchPageErrors } from "./fixtures/acceptance";

// AT-17 (PRD §13): a carton of mixed flavors is sold by its flavors. A carton holds a fixed number of
// parts, one part per chosen flavor; the quote refuses more parts than the cartons hold, changing the
// group refreshes the flavor table and the carton price, and the sale is delivered flavor by flavor so
// each flavor's on-hand drops by exactly the pieces it was given. The mix groups and flavors are made
// through the API with a unique suffix, so reruns on the same store do not collide.

interface Unit {
  id: string;
  name: string;
}
interface Sku {
  id: string;
  code: string;
}
interface ProductDetail {
  skus: Sku[];
}
interface MixGroup {
  id: string;
  name: string;
  pack_unit_id: string;
  units_per_pack: number;
  part_unit_id: string;
  units_per_part: number;
}
interface QuoteRow {
  id: string;
  doc_no: string;
  version: number;
  total_amount: string;
}
interface InvoiceLine {
  id: string;
  line_kind: string;
  sku_id: string | null;
  base_qty: string;
}
interface Invoice {
  id: string;
  version: number;
  total_amount: string;
  lines: InvoiceLine[];
}

/** Ids and names of one run's mix fixture: two groups over three flavors, with their units. */
interface MixFixture {
  pack: Unit;
  part: Unit;
  groupA: MixGroup;
  groupB: MixGroup;
  flavors: { a: Sku; b: Sku; c: Sku };
  prices: { a: string; b: string; c: string };
}

const FLAVOR_A = "Dâu";
const FLAVOR_B = "Socola";
const FLAVOR_C = "Vani";

test.beforeEach(() => requireOwner());

/**
 * Creates the units, three flavor products and two mix groups of this run. Group A holds 6 pieces a
 * carton in parts of 2 (3 parts), group B 4 pieces in parts of 2 (2 parts). Prices are per piece.
 */
async function createMixFixture(page: Page, suffix: string): Promise<MixFixture> {
  const unit = (name: string) => apiPost<Unit>(page, "/units", { name: `${name} ${suffix}` });
  const piece = await unit("Hộp");
  const pack = await unit("Thùng");
  const part = await unit("Dây");

  const prices = { a: "10000", b: "20000", c: "30000" };
  const flavor = async (letter: string, name: string, price: string): Promise<Sku> => {
    const created = await apiPost<{ product: ProductDetail }>(page, "/products:quick-create", {
      name: `Mix ${letter} ${suffix}`,
      code: `MX${letter}${suffix}`,
      base_unit_id: piece.id,
      sale_price: price,
    });
    const sku = created.product.skus[0];
    if (sku === undefined) throw new Error(`the product for flavor ${name} has no SKU`);
    return sku;
  };
  const flavors = {
    a: await flavor("A", FLAVOR_A, prices.a),
    b: await flavor("B", FLAVOR_B, prices.b),
    c: await flavor("C", FLAVOR_C, prices.c),
  };

  const groupA = await apiPost<MixGroup>(page, "/mix-groups", {
    name: `Thùng A ${suffix}`,
    pack_unit_id: pack.id,
    units_per_pack: 6,
    part_unit_id: part.id,
    units_per_part: 2,
    items: [
      { sku_id: flavors.a.id, flavor_label: FLAVOR_A },
      { sku_id: flavors.b.id, flavor_label: FLAVOR_B },
    ],
  });
  const groupB = await apiPost<MixGroup>(page, "/mix-groups", {
    name: `Thùng B ${suffix}`,
    pack_unit_id: pack.id,
    units_per_pack: 4,
    part_unit_id: part.id,
    units_per_part: 2,
    items: [{ sku_id: flavors.c.id, flavor_label: FLAVOR_C }],
  });
  return { pack, part, groupA, groupB, flavors, prices };
}

/** The accessible name of a control in the mix row of a group: "<text> – <group>, dòng N". */
function rowName(text: string, groupName: string): RegExp {
  return new RegExp(`^${escapeRe(text)} – ${escapeRe(groupName)}, dòng \\d+$`);
}

/** The flavor input of the mix row, named "Số <part> vị <flavor> – <group>, dòng N". */
function flavorInput(form: Locator, fx: MixFixture, group: MixGroup, flavor: string): Locator {
  return form.getByRole("textbox", { name: rowName(`Số ${fx.part.name} vị ${flavor}`, group.name) });
}

/** Chooses the flavors' parts of a mix row and its carton count. */
async function fillMixRow(form: Locator, fx: MixFixture, group: MixGroup, cartons: string, partsA: string, partsB: string): Promise<void> {
  await form.getByRole("textbox", { name: rowName("Số thùng", group.name) }).fill(cartons);
  await flavorInput(form, fx, group, FLAVOR_A).fill(partsA);
  await flavorInput(form, fx, group, FLAVOR_B).fill(partsB);
}

/**
 * The quote that a "Lưu & gửi khách" click stores and sends, read from the answer of its send: the
 * quote carries the id and the version it is at after the save. Register before the click.
 */
function sentQuote(page: Page): Promise<QuoteRow> {
  const answer = page.waitForResponse((r) => r.request().method() === "POST" && /\/quotes\/[^/]+:send$/.test(new URL(r.url()).pathname));
  return answer.then(async (r) => {
    expect(r.status(), `sending the quote answered ${r.status()}: ${await r.text()}`).toBeLessThan(300);
    return (await r.json()) as QuoteRow;
  });
}

test("sells a mixed-flavor carton by its flavors without letting one flavor go below zero", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  const suffix = uniqueSuffix();
  await page.goto("/catalog/mix-groups");
  const fx = await createMixFixture(page, suffix);
  for (const sku of [fx.flavors.a, fx.flavors.b, fx.flavors.c]) {
    await addStockApi(page, { sku_id: sku.id, qty: "20" }, `AT-17 stock ${suffix}`);
  }
  const customer = (await activeCustomers(page))[0];
  test.skip(customer === undefined, "the store has no active customer to quote");
  if (customer === undefined) throw new Error("unreachable");

  await page.goto("/sales/quotes");
  await test.step("the quote refuses more parts than the cartons hold", async () => {
    await page.getByRole("button", { name: "Tạo báo giá" }).click();
    const form = page.getByRole("dialog", { name: "Tạo báo giá" });
    await pickOption(form, form.getByRole("combobox", { name: "Khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));

    await form.getByRole("button", { name: "+ Thêm thùng mix vị" }).click();
    await form.getByRole("combobox", { name: /^Chọn nhóm mix vị/ }).selectOption(fx.groupA.id);
    // Group A holds 3 parts a carton: 2 + 2 parts in one carton is over the room.
    await fillMixRow(form, fx, fx.groupA, "1", "2", "2");
    await expect(form.getByText(/Giảm bớt 1 /)).toBeVisible();
    await form.getByRole("button", { name: "Lưu & gửi khách" }).click();
    await expect(errToast(page).filter({ hasText: /vượt/ })).toBeVisible();
    await expect(form).toBeVisible();

    await flavorInput(form, fx, fx.groupA, FLAVOR_B).fill("1");
    await expect(form.getByText(/đủ thùng/)).toBeVisible();
  });

  await test.step("changing the group refreshes the flavor table and the carton price", async () => {
    const form = page.getByRole("dialog", { name: "Tạo báo giá" });
    await form.getByRole("combobox", { name: /^Chọn nhóm mix vị/ }).selectOption(fx.groupB.id);
    await expect(flavorInput(form, fx, fx.groupB, FLAVOR_C)).toBeVisible();
    await expect(form.getByRole("textbox", { name: rowName(`Số ${fx.part.name} vị ${FLAVOR_A}`, fx.groupB.name) })).toHaveCount(0);
    // One flavor at 30 000 a piece, 4 pieces a carton: the carton is priced at 120 000.
    await expect(form.getByText(vnd(String(Number(fx.prices.c) * fx.groupB.units_per_pack)))).toBeVisible();

    await form.getByRole("combobox", { name: /^Chọn nhóm mix vị/ }).selectOption(fx.groupA.id);
    await fillMixRow(form, fx, fx.groupA, "1", "2", "1");
    await expect(form.getByText(/đủ thùng/)).toBeVisible();
  });

  let invoiceId = "";
  await test.step("the quote is saved and the customer's invoice is confirmed", async () => {
    const form = page.getByRole("dialog", { name: "Tạo báo giá" });
    const sent = sentQuote(page);
    await form.getByRole("button", { name: "Lưu & gửi khách" }).click();
    await expect(okToast(page).filter({ hasText: /^Đã lưu / })).toBeVisible();
    const quote = await sent;

    const converted = await apiPostRaw(page, `/quotes/${quote.id}:convert`, undefined, { "If-Match": `"${quote.version}"` });
    expect(converted.status, `converting the quote answered ${converted.status}: ${converted.text}`).toBe(201);
    const draft = JSON.parse(converted.text) as Invoice;
    invoiceId = draft.id;

    const confirmed = await apiPostRaw(page, `/invoices/${draft.id}:confirm`, {}, { "Idempotency-Key": randomUUID(), "If-Match": `"${draft.version}"` });
    expect(confirmed.status, `confirming the invoice answered ${confirmed.status}: ${confirmed.text}`).toBeLessThan(300);
  });

  // Each flavor takes its parts times the pieces of a part: group A's 2 parts of A and 1 part of B.
  const expectedOut = { a: dec(fx.groupA.units_per_part).times(2), b: dec(fx.groupA.units_per_part).times(1) };

  await test.step("the invoice is priced flavor by flavor", async () => {
    const invoice = await apiGet<Invoice>(page, `/invoices/${invoiceId}`);
    // A flavor's piece price times its pieces, added up over the carton: 2×2×10 000 + 1×2×20 000.
    const expectedTotal = dec(fx.prices.a).times(expectedOut.a).plus(dec(fx.prices.b).times(expectedOut.b));
    expect(dec(invoice.total_amount).eq(expectedTotal), `invoice total ${invoice.total_amount}, expected ${expectedTotal.toFixed()}`).toBe(true);
  });

  await test.step("the stock-out delivers the carton whole and takes exactly the pieces of each flavor", async () => {
    const invoice = await apiGet<Invoice>(page, `/invoices/${invoiceId}`);
    const header = invoice.lines.filter((l) => l.line_kind === "mix_pack");
    const flavorLines = invoice.lines.filter((l) => l.line_kind === "mix_flavor");
    expect(header.length, "the invoice carries one carton line").toBe(1);
    expect(flavorLines.length, "the invoice carries one line per chosen flavor").toBe(2);
    const [carton] = header;
    const [flavorLine] = flavorLines;
    if (carton === undefined || flavorLine === undefined) throw new Error("unreachable");

    // The server delivers a flavor only through its carton: a flavor line on its own is refused.
    const direct = await apiPostRaw(page, `/invoices/${invoiceId}/stock-outs`, {
      lines: [{ invoice_line_id: flavorLine.id, base_qty: flavorLine.base_qty }],
    }, { "Idempotency-Key": randomUUID() });
    expect(direct.status, `a flavor line on its own answered ${direct.status}: ${direct.text}`).toBe(422);
    expect(direct.text).toContain("deliver_through_parent");

    const before = {
      a: (await onHandByColor(page, fx.flavors.a.id))["uncolored"] ?? "0",
      b: (await onHandByColor(page, fx.flavors.b.id))["uncolored"] ?? "0",
      c: (await onHandByColor(page, fx.flavors.c.id))["uncolored"] ?? "0",
    };

    // A carton is delivered whole: its base_qty is the carton's remaining pieces, and a partial amount is refused.
    const out = await apiPostRaw(page, `/invoices/${invoiceId}/stock-outs`, {
      lines: [{ invoice_line_id: carton.id, base_qty: carton.base_qty }],
    }, { "Idempotency-Key": randomUUID() });
    expect(out.status, `the stock-out answered ${out.status}: ${out.text}`).toBe(201);

    const after = {
      a: (await onHandByColor(page, fx.flavors.a.id))["uncolored"] ?? "0",
      b: (await onHandByColor(page, fx.flavors.b.id))["uncolored"] ?? "0",
      c: (await onHandByColor(page, fx.flavors.c.id))["uncolored"] ?? "0",
    };
    expect(dec(before.a).minus(after.a).eq(expectedOut.a), `flavor A fell by ${dec(before.a).minus(after.a)}`).toBe(true);
    expect(dec(before.b).minus(after.b).eq(expectedOut.b), `flavor B fell by ${dec(before.b).minus(after.b)}`).toBe(true);
    expect(after.c, "flavor C is not in the carton and does not move").toBe(before.c);
  });

  await test.step("a carton short of parts is saved as chosen and priced by the parts chosen", async () => {
    await page.goto("/sales/quotes");
    await page.getByRole("button", { name: "Tạo báo giá" }).click();
    const form = page.getByRole("dialog", { name: "Tạo báo giá" });
    await pickOption(form, form.getByRole("combobox", { name: "Khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));
    await form.getByRole("button", { name: "+ Thêm thùng mix vị" }).click();
    await form.getByRole("combobox", { name: /^Chọn nhóm mix vị/ }).selectOption(fx.groupA.id);
    // Group A holds 3 parts a carton: 1 + 1 leaves 1 part empty, which the carton accepts.
    await fillMixRow(form, fx, fx.groupA, "1", "1", "1");
    await expect(form.getByText(/còn trống 1 /)).toBeVisible();
    const sent = sentQuote(page);
    await form.getByRole("button", { name: "Lưu & gửi khách" }).click();
    await expect(okToast(page).filter({ hasText: /^Đã lưu / })).toBeVisible();
    const quote = await sent;

    // Priced by the parts chosen: 1 part of A and 1 of B, each 2 pieces, at the piece price of its flavor.
    const pieces = dec(fx.groupA.units_per_part);
    const expectedQuote = dec(fx.prices.a).times(pieces).plus(dec(fx.prices.b).times(pieces));
    expect(dec(quote.total_amount).eq(expectedQuote), `quote total ${quote.total_amount}, expected ${expectedQuote.toFixed()}`).toBe(true);

    const converted = await apiPostRaw(page, `/quotes/${quote.id}:convert`, undefined, { "If-Match": `"${quote.version}"` });
    expect(converted.status, `converting the short carton answered ${converted.status}: ${converted.text}`).toBe(201);
    const draft = JSON.parse(converted.text) as Invoice;
    const confirmed = await apiPostRaw(page, `/invoices/${draft.id}:confirm`, {}, { "Idempotency-Key": randomUUID(), "If-Match": `"${draft.version}"` });
    expect(confirmed.status, `confirming the short carton answered ${confirmed.status}: ${confirmed.text}`).toBeLessThan(300);

    const before = {
      a: (await onHandByColor(page, fx.flavors.a.id))["uncolored"] ?? "0",
      b: (await onHandByColor(page, fx.flavors.b.id))["uncolored"] ?? "0",
    };
    const invoice = await apiGet<Invoice>(page, `/invoices/${draft.id}`);
    const carton = invoice.lines.find((l) => l.line_kind === "mix_pack");
    if (carton === undefined) throw new Error("the converted invoice has no carton line");
    const out = await apiPostRaw(page, `/invoices/${draft.id}/stock-outs`, {
      lines: [{ invoice_line_id: carton.id, base_qty: carton.base_qty }],
    }, { "Idempotency-Key": randomUUID() });
    expect(out.status, `the short carton's stock-out answered ${out.status}: ${out.text}`).toBe(201);

    const after = {
      a: (await onHandByColor(page, fx.flavors.a.id))["uncolored"] ?? "0",
      b: (await onHandByColor(page, fx.flavors.b.id))["uncolored"] ?? "0",
    };
    expect(dec(before.a).minus(after.a).eq(pieces), `flavor A fell by ${dec(before.a).minus(after.a)}`).toBe(true);
    expect(dec(before.b).minus(after.b).eq(pieces), `flavor B fell by ${dec(before.b).minus(after.b)}`).toBe(true);
  });

  await finishJourney(page, testInfo, errors);
});
