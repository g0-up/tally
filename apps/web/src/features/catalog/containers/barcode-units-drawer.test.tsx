import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { barcodeRow, barcodeServer } from "../../../test/msw/barcodes";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp, renderWithProviders } from "../../../test/render";
import { posKeys } from "../../pos";
import { UNIT_HOP, UNIT_THUNG, testSku } from "../../procurement/testing/fixtures";
import { skuKeys } from "../api/queries";
import type { SkuUnit } from "../schemas";
import { BarcodeUnitsDrawer } from "./barcode-units-drawer";

const UNIT_LOC = "01J0000000000000000000ULOC";
const UNIT_KET = "01J0000000000000000000UKET";

const unit = (unitId: string, name: string, factor: string, active = true): SkuUnit => ({
  unit_id: unitId,
  unit_name: name,
  factor,
  is_base: factor === "1",
  active,
  is_default_sale: false,
  is_default_purchase: false,
  retail_price: null,
  wholesale_price: null,
  locked_by: null,
});

const SKU = testSku({ id: "sku-milk", code: "SH01", display_name: "Sữa hộp" });
/** Units listed out of order, with a stopped "két". */
const PACKED_SKU = testSku({
  id: "sku-milk",
  code: "SH01",
  display_name: "Sữa hộp",
  units: [unit(UNIT_THUNG, "thùng", "12"), unit(UNIT_HOP, "hộp", "1"), unit(UNIT_KET, "két", "24", false), unit(UNIT_LOC, "lốc", "4")],
});
const BOX = barcodeRow("bc-box", "8935001234567", UNIT_HOP);
const CARTON = barcodeRow("bc-carton", "18935001234564", UNIT_HOP);

function held() {
  let release!: () => void;
  const answer = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { answer, release };
}

const renderDrawer = (sku = SKU) => renderWithProviders(<BarcodeUnitsDrawer sku={sku} onClose={() => {}} />);
const picker = (code: string) => screen.findByRole("combobox", { name: `Đơn vị của mã ${code}` });
const saveName = (code: string) => `Lưu đơn vị của mã ${code}`;
const saveButton = (code: string) => screen.queryByRole("button", { name: saveName(code) });
const getSave = (code: string) => screen.getByRole("button", { name: saveName(code) });
const optionTexts = (select: HTMLElement) => within(select).getAllByRole("option").map((o) => o.textContent);

/**
 * ArrowDown on a closed select, as Chrome and Firefox do it on Windows and Linux: the next option is
 * selected and "change" fires at once. jsdom does not move a closed select, so the change the
 * browser would fire follows the real key press.
 */
async function arrowDown(user: UserEvent, select: HTMLSelectElement) {
  await user.keyboard("{ArrowDown}");
  const next = select.options[select.selectedIndex + 1];
  if (next) fireEvent.change(select, { target: { value: next.value } });
}

describe("BarcodeUnitsDrawer", () => {
  it("lists each barcode of the SKU with its unit, offering the SKU's units", async () => {
    barcodeServer({ [SKU.id]: [BOX, { ...CARTON, unit_id: UNIT_THUNG }] });
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Mã vạch · Sữa hộp" });
    const table = await within(drawer).findByRole("table", { name: "Mã vạch của Sữa hộp" });
    expect(within(table).getByText("8935001234567")).toBeInTheDocument();
    expect(within(table).getByText("18935001234564")).toBeInTheDocument();
    expect(await picker("8935001234567")).toHaveValue(UNIT_HOP);
    const carton = await picker("18935001234564");
    expect(carton).toHaveValue(UNIT_THUNG);
    expect(optionTexts(carton)).toEqual(["hộp", "thùng (12 hộp)"]);
    expect(saveButton("18935001234564")).not.toBeInTheDocument();
  });

  it("offers active units smallest first, and a stopped unit only to the barcode that still uses it", async () => {
    barcodeServer({ [SKU.id]: [BOX, barcodeRow("bc-crate", "28935001234561", UNIT_KET)] });
    renderDrawer(PACKED_SKU);

    expect(optionTexts(await picker("8935001234567"))).toEqual(["hộp", "lốc (4 hộp)", "thùng (12 hộp)"]);
    const crate = await picker("28935001234561");
    expect(optionTexts(crate)).toEqual(["hộp", "lốc (4 hộp)", "thùng (12 hộp)", "két (24 hộp)"]);
    expect(crate).toHaveValue(UNIT_KET);
  });

  it("sends nothing while the unit is only picked, then saves it on Lưu, holding the row while waiting", async () => {
    const api = barcodeServer({ [SKU.id]: [BOX, CARTON] });
    const reply = held();
    api.onPatch(async () => {
      await reply.answer;
      return undefined;
    });
    renderDrawer();
    const user = userEvent.setup();
    const carton = await picker("18935001234564");

    await user.selectOptions(carton, UNIT_THUNG);
    expect(carton).toHaveValue(UNIT_THUNG);
    expect(api.patches).toEqual([]);
    // Picking the saved unit again drops the change.
    await user.selectOptions(carton, UNIT_HOP);
    expect(saveButton("18935001234564")).not.toBeInTheDocument();
    await user.selectOptions(carton, UNIT_THUNG);

    const save = getSave("18935001234564");
    expect(save).toHaveTextContent("Lưu");
    await user.click(save);
    await waitFor(() => expect(api.patches).toEqual([{ skuId: SKU.id, barcodeId: CARTON.id, body: { unit_id: UNIT_THUNG } }]));
    expect(save).toHaveAttribute("aria-disabled", "true");
    expect(save).toHaveTextContent("Đang lưu…");
    expect(save).toHaveFocus();
    expect(carton).toHaveAttribute("aria-disabled", "true");
    // Neither a second press nor a pick made while the change is in flight sends anything.
    await user.click(save);
    await user.selectOptions(carton, UNIT_HOP);
    expect(api.patches).toHaveLength(1);
    expect(carton).toHaveValue(UNIT_THUNG);

    reply.release();
    expect(await screen.findByText(/nay là mã của thùng/)).toBeInTheDocument();
    await waitFor(() => expect(carton).not.toHaveAttribute("aria-disabled"));
    expect(carton).toHaveValue(UNIT_THUNG);
    expect(saveButton("18935001234564")).not.toBeInTheDocument();
    expect(carton).toHaveFocus();
    expect(await picker("8935001234567")).toHaveValue(UNIT_HOP);
  });

  it("does not save the units arrow keys pass through until Lưu is pressed", async () => {
    const api = barcodeServer({ [SKU.id]: [BOX] });
    renderDrawer(PACKED_SKU);
    const user = userEvent.setup();
    const box = (await picker("8935001234567")) as HTMLSelectElement;

    box.focus();
    await arrowDown(user, box);
    expect(box).toHaveValue(UNIT_LOC);
    await arrowDown(user, box);
    expect(box).toHaveValue(UNIT_THUNG);
    expect(api.patches).toEqual([]);

    await user.tab();
    expect(saveButton("8935001234567")).toHaveFocus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(api.patches).toEqual([{ skuId: SKU.id, barcodeId: BOX.id, body: { unit_id: UNIT_THUNG } }]));
    expect(await screen.findByText(/nay là mã của thùng/)).toBeInTheDocument();
  });

  it("puts the old unit back and says why under the picker when the change is refused", async () => {
    const api = barcodeServer({ [SKU.id]: [BOX, CARTON] });
    api.onPatch(() => apiError(422, "validation_failed", { fields: { unit_id: "not_a_unit_of_sku" } }));
    renderDrawer();
    const user = userEvent.setup();
    const carton = await picker("18935001234564");

    await user.selectOptions(carton, UNIT_THUNG);
    await user.click(getSave("18935001234564"));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Đơn vị này không còn thuộc hàng hóa. Tải lại để xem bản mới nhất.");
    expect(carton).toHaveValue(UNIT_HOP);
    expect(carton).toHaveAttribute("aria-invalid", "true");
    expect(carton).toHaveAccessibleDescription("Đơn vị này không còn thuộc hàng hóa. Tải lại để xem bản mới nhất.");
    expect(carton).not.toHaveAttribute("aria-disabled");
    expect(carton).toHaveFocus();
    expect(saveButton("18935001234564")).not.toBeInTheDocument();
    expect(api.barcodes.get(SKU.id)?.find((b) => b.id === CARTON.id)?.unit_id).toBe(UNIT_HOP);
  });

  it("refreshes the SKU lists and the POS bootstrap once a unit is saved", async () => {
    barcodeServer({ [SKU.id]: [CARTON] });
    const { platform } = renderDrawer();
    const listKey = skuKeys.list({});
    platform.queryClient.setQueryData(listKey, { pages: [], pageParams: [] });
    platform.queryClient.setQueryData(posKeys.bootstrap(), { data: null });
    const user = userEvent.setup();

    await user.selectOptions(await picker("18935001234564"), UNIT_THUNG);
    expect(platform.queryClient.getQueryState(posKeys.bootstrap())?.isInvalidated).toBe(false);
    await user.click(getSave("18935001234564"));

    await screen.findByText(/nay là mã của thùng/);
    expect(platform.queryClient.getQueryState(posKeys.bootstrap())?.isInvalidated).toBe(true);
    expect(platform.queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
  });

  it("names no unit in the toast when the saved unit is not among the SKU's units", async () => {
    const api = barcodeServer({ [SKU.id]: [CARTON] });
    api.onPatch(() => HttpResponse.json({ ...CARTON, unit_id: UNIT_LOC }));
    renderDrawer();
    const user = userEvent.setup();

    await user.selectOptions(await picker("18935001234564"), UNIT_THUNG);
    await user.click(getSave("18935001234564"));

    expect(await screen.findByText(/Đã đổi đơn vị của mã/)).toHaveTextContent("Đã đổi đơn vị của mã 18935001234564.");
    expect(screen.queryByText(/nay là mã của/)).not.toBeInTheDocument();
  });

  it("says so when the SKU has no barcode", async () => {
    barcodeServer({});
    renderDrawer();
    const drawer = await screen.findByRole("dialog", { name: "Mã vạch · Sữa hộp" });
    expect(await within(drawer).findByText("Hàng này chưa có mã vạch.")).toBeInTheDocument();
    expect(within(drawer).queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("opens from the Mã vạch button of the SKU's row in Hàng hóa and follows the SKU as the list holds it now", async () => {
    barcodeServer({ [SKU.id]: [BOX] });
    let listed = SKU;
    server.use(
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [listed], next_cursor: null })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    );
    const { platform } = renderApp("/catalog/products");
    const table = await screen.findByRole("table", { name: "Hàng hóa" });
    const button = within(table).getByRole("button", { name: "Mã vạch Sữa hộp" });
    expect(button.closest("td")).toHaveClass("acts");

    await userEvent.setup().click(button);
    const drawer = await screen.findByRole("dialog", { name: "Mã vạch · Sữa hộp" });
    const box = await within(drawer).findByRole("combobox", { name: "Đơn vị của mã 8935001234567" });
    expect(box).toHaveValue(UNIT_HOP);

    // A unit added elsewhere reaches the open drawer once the list refetches.
    listed = { ...SKU, units: [...SKU.units, unit(UNIT_LOC, "lốc", "4")] };
    await platform.queryClient.invalidateQueries({ queryKey: skuKeys.all });
    await waitFor(() => expect(optionTexts(box)).toEqual(["hộp", "lốc (4 hộp)", "thùng (12 hộp)"]));

    // A SKU that leaves the list closes the drawer, which stays closed when the SKU comes back.
    listed = { ...SKU, id: "sku-other", display_name: "Sữa khác" };
    await platform.queryClient.invalidateQueries({ queryKey: skuKeys.all });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Mã vạch · Sữa hộp" })).not.toBeInTheDocument());
    listed = SKU;
    await platform.queryClient.invalidateQueries({ queryKey: skuKeys.all });
    expect(await within(table).findByRole("button", { name: "Mã vạch Sữa hộp" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Mã vạch · Sữa hộp" })).not.toBeInTheDocument();
  });
});
