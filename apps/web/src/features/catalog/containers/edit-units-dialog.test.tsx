import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { toMoney } from "../../../shared/domain/money";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { posKeys } from "../../pos";
import { skuKeys } from "../api/queries";
import { UNIT_EDIT_MESSAGES } from "../components/product-draft";
import type { SkuDetail, SkuUnit } from "../schemas";
import { EditUnitsDialog } from "./edit-units-dialog";

const CAI = "01J00000000000000000000UCAI";
const DAY = "01J00000000000000000000UDAY";
const HOP = "01J00000000000000000000UHOP";
const GOI = "01J00000000000000000000UGOI";
const LOC = "01J00000000000000000000ULOC";
const THUNG = "01J0000000000000000000UTHUNG";
const UNITS = [
  { id: CAI, name: "cái", active: true },
  { id: DAY, name: "dây", active: true },
  { id: HOP, name: "hộp", active: true },
  { id: GOI, name: "gói", active: true },
  { id: LOC, name: "lốc", active: true },
  { id: THUNG, name: "thùng", active: true },
];

const unit = (unitId: string, name: string, factor: string, over: Partial<SkuUnit> = {}): SkuUnit => ({
  unit_id: unitId,
  unit_name: name,
  factor,
  is_base: factor === "1",
  active: true,
  is_default_sale: false,
  is_default_purchase: false,
  retail_price: null,
  wholesale_price: null,
  locked_by: null,
  ...over,
});

/**
 * Kẹo counted in cái at 500 (sỉ 450): dây of 50 used on receipts and scanned by a barcode, with its
 * own retail price; hộp with price history; gói scanned by a barcode; lốc never used; thùng used on
 * receipts and bought by default.
 */
function keo(over: Partial<SkuDetail> = {}): SkuDetail {
  return {
    id: "sku-keo",
    display_name: "Kẹo dẻo",
    base_unit_id: CAI,
    version: 3,
    units: [
      unit(CAI, "cái", "1", { retail_price: toMoney(500), wholesale_price: toMoney(450), is_default_sale: true }),
      unit(DAY, "dây", "50", { retail_price: toMoney(24000), locked_by: "documents" }),
      unit(HOP, "hộp", "20", { locked_by: "price_history" }),
      unit(GOI, "gói", "10", { locked_by: "barcodes" }),
      unit(LOC, "lốc", "5"),
      unit(THUNG, "thùng", "1000", { locked_by: "documents", is_default_purchase: true }),
    ],
    barcodes: [
      { id: "bc-day", unit_id: DAY, code: "8930000000011" },
      { id: "bc-goi", unit_id: GOI, code: "8930000000028" },
    ],
    ...over,
  };
}

interface PutCall {
  ifMatch: string | null;
  body: unknown;
}

/** The SKU read (answering `reads` in turn, the last one again) and every PUT of its units. */
function unitsServer(reads: SkuDetail[], answer: (call: PutCall, n: number) => Response | undefined = () => undefined) {
  const puts: PutCall[] = [];
  let read = 0;
  server.use(
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: UNITS, next_cursor: null })),
    http.get(apiUrl("/skus/sku-keo"), () => HttpResponse.json(reads[Math.min(read++, reads.length - 1)])),
    http.put(apiUrl("/skus/sku-keo/units"), async ({ request }) => {
      const call = { ifMatch: request.headers.get("If-Match"), body: await request.json() };
      puts.push(call);
      return answer(call, puts.length) ?? HttpResponse.json({ ...keo(), version: 4 });
    }),
  );
  return { puts, reads: () => read };
}

async function renderDialog() {
  const onClose = vi.fn();
  const user = userEvent.setup();
  const app = renderWithProviders(<EditUnitsDialog sku={{ id: "sku-keo", display_name: "Kẹo dẻo" }} onClose={onClose} />);
  // The loading dialog gives way to the form's own once the SKU is read.
  await screen.findByRole("group", { name: "Đơn vị tính & giá bán" });
  const dialog = screen.getByRole("dialog", { name: "Đơn vị & giá · Kẹo dẻo" });
  return { ...app, dialog, onClose, user };
}

const rowOf = (dialog: HTMLElement, name: string) => {
  const row = within(dialog).getByLabelText(`Giá bán lẻ / ${name}`).closest("tr");
  if (row === null) throw new Error(`no row for ${name}`);
  return row;
};

const save = (dialog: HTMLElement) => within(dialog).getByRole("button", { name: "Lưu đơn vị & giá" });

describe("EditUnitsDialog", () => {
  it("fills the table from the SKU and names why each used unit cannot be deleted", async () => {
    unitsServer([keo()]);
    const { dialog } = await renderDialog();

    expect(dialog).toHaveTextContent("Áp dụng cho giao dịch mới. Chứng từ đã lập giữ đơn vị, hệ số và giá đã chụp.");
    expect(within(dialog).getByLabelText("Giá bán lẻ / cái")).toHaveValue("500");
    expect(within(dialog).getByLabelText("Giá sỉ / cái")).toHaveValue("450");
    expect(within(dialog).getByLabelText("Giá bán lẻ / dây")).toHaveValue("24.000");
    expect(within(dialog).getByLabelText("Giá sỉ / dây")).toHaveValue("");
    expect(within(dialog).getByLabelText("Quy đổi: 1 thùng bằng bao nhiêu cái")).toHaveValue("1.000");

    expect(rowOf(dialog, "dây")).toHaveTextContent("Đã dùng trên chứng từ. Không xóa được, chọn Ngừng dùng.");
    expect(rowOf(dialog, "hộp")).toHaveTextContent("Đã có lịch sử giá. Không xóa được, chọn Ngừng dùng.");
    expect(rowOf(dialog, "gói")).toHaveTextContent("Có mã vạch. Không xóa được, chọn Ngừng dùng.");
    for (const name of ["dây", "hộp", "gói", "thùng"]) {
      const row = rowOf(dialog, name);
      expect(within(row).getByRole("checkbox", { name: `Ngừng dùng ${name}` })).not.toBeChecked();
      expect(within(row).queryByRole("button", { name: `Xóa đơn vị ${name}` })).not.toBeInTheDocument();
    }
    const loc = rowOf(dialog, "lốc");
    expect(within(loc).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(loc).getByRole("button", { name: "Xóa đơn vị lốc" })).toBeInTheDocument();
    expect(rowOf(dialog, "thùng")).toHaveTextContent("Đơn vị nhập mặc định.");
  });

  it("warns that old documents keep the old factor and that counts scanning the code use the new one", async () => {
    unitsServer([keo()]);
    const { dialog, user } = await renderDialog();
    const factor = within(dialog).getByLabelText("Quy đổi: 1 dây bằng bao nhiêu cái");

    await user.clear(factor);
    await user.type(factor, "40");

    const row = rowOf(dialog, "dây");
    expect(row).toHaveTextContent("Chứng từ cũ giữ hệ số cũ (50).");
    expect(row).toHaveTextContent("Kiểm kê quét mã này sẽ cộng theo hệ số mới.");
  });

  it("warns that a stopped unit's barcode no longer scans", async () => {
    unitsServer([keo()]);
    const { dialog, user } = await renderDialog();
    const hint = "Mã vạch của đơn vị này sẽ không quét được ở bán hàng và kiểm kê.";

    await user.click(within(rowOf(dialog, "hộp")).getByRole("checkbox", { name: "Ngừng dùng hộp" }));
    expect(rowOf(dialog, "hộp")).not.toHaveTextContent(hint);

    await user.click(within(rowOf(dialog, "gói")).getByRole("checkbox", { name: "Ngừng dùng gói" }));
    expect(rowOf(dialog, "gói")).toHaveTextContent(hint);
    expect(rowOf(dialog, "gói")).toHaveClass("ut-dim");
  });

  it("names the unit purchases move to when the default purchase unit is stopped", async () => {
    unitsServer([keo()]);
    const { dialog, user } = await renderDialog();

    await user.click(within(rowOf(dialog, "thùng")).getByRole("checkbox", { name: "Ngừng dùng thùng" }));

    expect(rowOf(dialog, "thùng")).toHaveTextContent("Đơn vị nhập mặc định chuyển sang dây.");
    expect(rowOf(dialog, "dây")).toHaveTextContent("Đơn vị nhập mặc định.");
  });

  it("saves the whole table against the SKU version, sending a cleared unit price and untouched base prices as null", async () => {
    const api = unitsServer([keo()]);
    const { dialog, onClose, platform, user } = await renderDialog();
    const invalidate = vi.spyOn(platform.queryClient, "invalidateQueries");

    await user.clear(within(dialog).getByLabelText("Giá bán lẻ / dây"));
    await user.click(within(rowOf(dialog, "gói")).getByRole("checkbox", { name: "Ngừng dùng gói" }));
    await user.click(save(dialog));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.puts).toEqual([
      {
        ifMatch: '"3"',
        body: {
          retail_price: null,
          wholesale_price: null,
          units: [
            { unit_id: DAY, factor: "50", active: true, retail_price: null, wholesale_price: null },
            { unit_id: HOP, factor: "20", active: true, retail_price: null, wholesale_price: null },
            { unit_id: GOI, factor: "10", active: false, retail_price: null, wholesale_price: null },
            { unit_id: LOC, factor: "5", active: true, retail_price: null, wholesale_price: null },
            { unit_id: THUNG, factor: "1000", active: true, retail_price: null, wholesale_price: null },
          ],
        },
      },
    ]);
    expect(await screen.findByText("Đã lưu đơn vị & giá. Bán hàng, báo giá và đơn hàng mới dùng ngay.")).toBeInTheDocument();
    const invalidated = invalidate.mock.calls.map(([filters]) => filters?.queryKey);
    expect(invalidated).toEqual(expect.arrayContaining([skuKeys.all, ["pricing"], posKeys.all, ["sales", "lookup", "sku"], ["inventory", "stock"], ["inventory", "stock-card"]]));
  });

  it("refuses a save that changes nothing without sending it", async () => {
    const api = unitsServer([keo()]);
    const { dialog, onClose, user } = await renderDialog();

    await user.click(save(dialog));

    expect(await screen.findByText(UNIT_EDIT_MESSAGES.unchanged, { exact: false })).toBeInTheDocument();
    expect(api.puts).toEqual([]);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("keeps the draft when the SKU changed elsewhere, and saves it against the reloaded version", async () => {
    const api = unitsServer([keo(), keo({ version: 5 })], (_, n) => (n === 1 ? apiError(412, "precondition_failed") : undefined));
    const { dialog, onClose, user } = await renderDialog();
    const retail = within(dialog).getByLabelText("Giá bán lẻ / hộp");

    await user.type(retail, "9800");
    await user.click(save(dialog));

    expect(await screen.findByText(STALE_DATA_MESSAGE)).toBeInTheDocument();
    await waitFor(() => expect(api.reads()).toBeGreaterThan(1));
    expect(onClose).not.toHaveBeenCalled();
    expect(within(dialog).getByLabelText("Giá bán lẻ / hộp")).toHaveValue("9.800");

    await user.click(save(dialog));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.puts.map((p) => p.ifMatch)).toEqual(['"3"', '"5"']);
  });

  it("puts a removed unit the server says is in use back as stopped, marked on its row", async () => {
    const api = unitsServer([keo()], (_, n) => (n === 1 ? apiError(409, "unit_in_use", { fields: { [`units[${LOC}]`]: "documents" } }) : undefined));
    const { dialog, onClose, user } = await renderDialog();

    await user.click(within(rowOf(dialog, "lốc")).getByRole("button", { name: "Xóa đơn vị lốc" }));
    expect(within(dialog).queryByLabelText("Giá bán lẻ / lốc")).not.toBeInTheDocument();
    await user.click(save(dialog));

    const loc = await waitFor(() => rowOf(dialog, "lốc"));
    expect(loc).toHaveTextContent("Đã dùng trên chứng từ. Không xóa được, chọn Ngừng dùng.");
    expect(loc).toHaveTextContent(UNIT_EDIT_MESSAGES.inUse("Đã dùng trên chứng từ"));
    expect(within(loc).getByRole("checkbox", { name: "Ngừng dùng lốc" })).toBeChecked();
    expect(within(loc).queryByRole("button", { name: "Xóa đơn vị lốc" })).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(save(dialog));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const units = (api.puts[1]?.body as { units: { unit_id: string; active: boolean }[] }).units;
    expect(units.find((u) => u.unit_id === LOC)).toMatchObject({ active: false });
  });

  it("shows a refused row field under its row", async () => {
    unitsServer([keo()], () => apiError(422, "validation_failed", { fields: { "units[3].unit_id": "duplicate" } }));
    const { dialog, onClose, user } = await renderDialog();

    await user.type(within(dialog).getByLabelText("Giá bán lẻ / lốc"), "2400");
    await user.click(save(dialog));

    await waitFor(() => expect(rowOf(dialog, "lốc")).toHaveTextContent("Đơn vị bị trùng."));
    expect(onClose).not.toHaveBeenCalled();
  });
});
