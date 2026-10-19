import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testSku } from "../../procurement/testing/fixtures";
import { recipeEditorHandlers, testKit, testMixGroup, UNIT_GOI } from "../testing/fixtures";
import { KIT_MESSAGES } from "../api/adapters";
import { apiUrl } from "../../../test/msw/handlers";
import { RecipeVersionDialog } from "./recipe-version-dialog";

const kit = testKit({ status: "active", active_version: 1, latest: { ...testKit().latest, status: "active", note: "Mẫu đầu mùa" } });
const skus = [testSku(), testSku({ id: "sku-kit", code: "GQAK", display_name: "Giỏ An Khang", retail_price: toMoney(500000) })];

describe("RecipeVersionDialog", () => {
  it("asks why the recipe changes, then saves and publishes the new version against the kit's version", async () => {
    const saved: { body: unknown; ifMatch: string | null }[] = [];
    const published: (string | null)[] = [];
    const v2 = testKit({ status: "active", active_version: 1, version: 2, latest: { ...kit.latest, id: "rcp-2", version_no: 2, status: "draft" } });
    server.use(
      ...recipeEditorHandlers(skus),
      http.post(/\/kits\/kit-1\/recipes$/, async ({ request }) => {
        saved.push({ body: await request.json(), ifMatch: request.headers.get("If-Match") });
        return HttpResponse.json(v2, { status: 201 });
      }),
      http.post(/\/kits\/kit-1:publish$/, ({ request }) => {
        published.push(request.headers.get("If-Match"));
        return HttpResponse.json({ ...v2, active_version: 2, version: 3, latest: { ...v2.latest, status: "active" } });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<RecipeVersionDialog kit={kit} onClose={onClose} />);

    // The form mounts once the SKUs it edits are loaded; until then a pending dialog shares the title.
    expect(await screen.findByRole("combobox", { name: "Thành phần 1" })).toHaveValue("SP001 · Bánh quy bơ");
    const dialog = screen.getByRole("dialog", { name: "Phiên bản mới cho Giỏ An Khang" });
    const note = within(dialog).getByRole("textbox", { name: "Lý do thay đổi (bắt buộc)" });
    expect(note).toHaveValue("");
    await user.click(within(dialog).getByRole("button", { name: "Lưu phiên bản" }));
    expect(await screen.findByText("Nhập lý do thay đổi.")).toBeInTheDocument();
    expect(saved).toEqual([]);

    await user.type(note, "Thêm hộp bánh");
    await user.click(within(dialog).getByRole("button", { name: "Lưu phiên bản" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(saved).toEqual([{ body: { items: [{ kind: "goods", sku_id: "sku-1", qty: "3" }], packing_cost: "20000", note: "Thêm hộp bánh" }, ifMatch: etagOf(1) }]);
    expect(published).toEqual([etagOf(2)]);
  });

  it("publishes the saved version on retry instead of adding another one", async () => {
    let versions = 0;
    let publishes = 0;
    const v2 = testKit({ status: "active", active_version: 1, version: 2, latest: { ...kit.latest, id: "rcp-2", version_no: 2, status: "draft" } });
    server.use(
      ...recipeEditorHandlers(skus),
      http.post(/\/kits\/kit-1\/recipes$/, () => {
        versions += 1;
        return HttpResponse.json(v2, { status: 201 });
      }),
      http.post(/\/kits\/kit-1:publish$/, () => {
        publishes += 1;
        return publishes === 1 ? HttpResponse.error() : HttpResponse.json({ ...v2, active_version: 2, version: 3, latest: { ...v2.latest, status: "active" } });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<RecipeVersionDialog kit={kit} onClose={onClose} />);

    await screen.findByRole("combobox", { name: "Thành phần 1" });
    const dialog = screen.getByRole("dialog", { name: "Phiên bản mới cho Giỏ An Khang" });
    await user.type(within(dialog).getByRole("textbox", { name: "Lý do thay đổi (bắt buộc)" }), "Thêm hộp bánh");
    await user.click(within(dialog).getByRole("button", { name: "Lưu phiên bản" }));

    expect(await screen.findByText(/Đã lưu v2 nhưng chưa phát hành phiên bản mới/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Lưu phiên bản" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(versions).toBe(1);
    expect(publishes).toBe(2);
  });

  it("shows a mix line whose group was stopped and asks to replace it before saving", async () => {
    const saved: unknown[] = [];
    const stopped = testMixGroup({ name: "Kẹo Tết", status: "inactive" });
    const mixed = testKit({
      status: "active",
      active_version: 1,
      latest: {
        ...kit.latest,
        items: [{ id: "ri-2", line_no: 1, kind: "mix_pack", mix_group_id: "mix-1", unit_id: UNIT_GOI, qty: "3", base_qty: "3", flavors: [{ sku_id: "sku-dau", parts: "3" }] }],
      },
    });
    server.use(
      // Listed first: within one call the earlier handler answers.
      http.get(apiUrl("/mix-groups"), () => HttpResponse.json({ data: [stopped], next_cursor: null })),
      ...recipeEditorHandlers(skus),
      http.post(/\/kits\/kit-1\/recipes$/, async ({ request }) => {
        saved.push(await request.json());
        return HttpResponse.json(mixed, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<RecipeVersionDialog kit={mixed} onClose={vi.fn()} />);

    expect(await screen.findByText("Nhóm mix đã ngừng")).toBeInTheDocument();
    const dialog = screen.getByRole("dialog", { name: "Phiên bản mới cho Giỏ An Khang" });
    expect(within(dialog).getByRole("combobox", { name: "Chọn nhóm mix vị thành phần 1" })).toHaveDisplayValue("Kẹo Tết (đã ngừng)");
    expect(within(dialog).queryByRole("button", { name: "+ Thêm thùng mix vị" })).not.toBeInTheDocument();
    await user.type(within(dialog).getByRole("textbox", { name: "Lý do thay đổi (bắt buộc)" }), "Đổi vị");
    await user.click(within(dialog).getByRole("button", { name: "Lưu phiên bản" }));

    expect(await screen.findByText(KIT_MESSAGES.mixGroupStopped("Kẹo Tết"), { exact: false })).toBeInTheDocument();
    expect(saved).toEqual([]);
  });
});
