import { useEffect, useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { MAX_UNIT_ROWS, UNIT_MESSAGES, unitRow, type ProductErrors, type UnitTable } from "./product-draft";
import { UnitPricePreview, UnitPriceTable, type UnitPriceTableProps } from "./unit-price-table";

const CAI = "unit-cai";
const DAY = "unit-day";
const THUNG = "unit-thung";
const UNITS = [
  { id: CAI, name: "cái", active: true },
  { id: DAY, name: "dây", active: true },
  { id: THUNG, name: "thùng", active: true },
];

/** Counted in cái at 500, sold by the dây of 50. */
const keo = (): UnitTable => ({
  baseUnitId: CAI,
  retailPrice: toMoney(500),
  wholesalePrice: toMoney(450),
  units: [unitRow({ unitId: DAY, unitName: "dây", factor: toQty(50) })],
});

/** The table as its form holds it, after the last change. */
const seen: { value?: UnitTable } = {};
const current = () => {
  if (seen.value === undefined) throw new Error("table not rendered");
  return seen.value;
};

function Harness({ initial, errors = {}, ...props }: { initial: UnitTable; errors?: ProductErrors } & Pick<UnitPriceTableProps, "baseUnitName" | "purchaseUnitId">) {
  const [value, setValue] = useState(initial);
  useEffect(() => {
    seen.value = value;
  });
  return (
    <>
      <UnitPriceTable value={value} onChange={(patch) => setValue((v) => ({ ...v, ...patch }))} errors={errors} {...props} />
      <UnitPricePreview value={value} {...(props.baseUnitName === undefined ? {} : { baseUnitName: props.baseUnitName })} />
    </>
  );
}

async function renderTable(props: Parameters<typeof Harness>[0]) {
  server.use(http.get(apiUrl("/units"), () => HttpResponse.json({ data: UNITS })));
  const user = userEvent.setup();
  const { platform } = renderWithProviders(<Harness {...props} />);
  await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));
  return { user, platform };
}

describe("UnitPriceTable", () => {
  it("shows a blank unit price as base price × factor, and keeps it blank", async () => {
    const { user } = await renderTable({ initial: keo() });
    const retail = screen.getByRole("textbox", { name: "Giá bán lẻ / dây" });
    expect(retail).toHaveValue("");
    expect(retail).toHaveAttribute("placeholder", "25.000 tự tính");
    expect(screen.getByRole("textbox", { name: "Giá sỉ / dây" })).toHaveAttribute("placeholder", "22.500 tự tính");

    await user.clear(screen.getByRole("textbox", { name: "Giá bán lẻ / cái" }));
    await user.type(screen.getByRole("textbox", { name: "Giá bán lẻ / cái" }), "600");
    expect(retail).toHaveAttribute("placeholder", "30.000 tự tính");
    expect(current().units[0]?.retail).toBeNull();
    expect(screen.getByText("1 dây = 50 cái")).toBeInTheDocument();
  });

  it("keeps a found error on its own row when an earlier row is removed", async () => {
    const t = keo();
    const initial = { ...t, units: [...t.units, unitRow({ unitId: THUNG, unitName: "thùng", factor: toQty(0) })] };
    const { user } = await renderTable({ initial, errors: { "units.1.factor": "Hệ số phải lớn hơn 0." } });
    expect(screen.getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu cái" })).toHaveAttribute("aria-invalid", "true");

    await user.click(screen.getByRole("button", { name: "Xóa đơn vị dây" }));

    expect(screen.getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu cái" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Hệ số phải lớn hơn 0.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Xóa đơn vị thùng" }));

    expect(screen.queryByText("Hệ số phải lớn hơn 0.")).not.toBeInTheDocument();
  });

  it("adds a row with its unit field focused, picks a listed unit and removes the row", async () => {
    const { user } = await renderTable({ initial: keo() });
    await user.click(screen.getByRole("button", { name: "+ Thêm đơn vị" }));
    const field = screen.getByRole("combobox", { name: "Tên đơn vị 3" });
    await waitFor(() => expect(field).toHaveFocus());
    await user.type(field, "thùng");
    await user.click(await screen.findByRole("option", { name: "thùng" }));
    await user.type(screen.getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu cái" }), "500");
    expect(current().units.map((u) => [u.unitId, u.factor])).toEqual([
      [DAY, "50"],
      [THUNG, "500"],
    ]);

    await user.click(screen.getByRole("button", { name: "Xóa đơn vị dây" }));
    expect(current().units.map((u) => u.unitId)).toEqual([THUNG]);
    expect(screen.getByRole("button", { name: "+ Thêm đơn vị" })).toHaveFocus();
  });

  it("adds a unit missing from the directory, saying so while it is added", async () => {
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const posted: unknown[] = [];
    server.use(
      http.post(apiUrl("/units"), async ({ request }) => {
        posted.push(await request.json());
        await held;
        return HttpResponse.json({ id: "unit-loc", name: "lốc", active: true }, { status: 201 });
      }),
    );
    const { user } = await renderTable({ initial: keo() });
    await user.click(screen.getByRole("button", { name: "+ Thêm đơn vị" }));
    await user.type(screen.getByRole("combobox", { name: "Tên đơn vị 3" }), "lốc");
    await user.click(await screen.findByRole("option", { name: "+ Tạo mới “lốc”" }));

    expect(await screen.findByText("Đơn vị mới, sẽ thêm vào danh mục đơn vị.")).toBeInTheDocument();
    expect(current().units[1]).toMatchObject({ unitId: null, unitName: "lốc" });
    release();
    await waitFor(() => expect(current().units[1]).toMatchObject({ unitId: "unit-loc", unitName: "lốc" }));
    expect(screen.queryByText("Đơn vị mới, sẽ thêm vào danh mục đơn vị.")).not.toBeInTheDocument();
    expect(posted).toEqual([{ name: "lốc" }]);
  });

  it("stops adding rows once the product holds as many units as it may, and shows a refusal of the table", async () => {
    const units = Array.from({ length: MAX_UNIT_ROWS }, (_, i) => unitRow({ unitId: `unit-${i}`, unitName: `đv ${i}`, factor: toQty(i + 2) }));
    const { user } = await renderTable({ initial: { ...keo(), units }, errors: { units: UNIT_MESSAGES.tooMany } });
    const add = screen.getByRole("button", { name: "+ Thêm đơn vị" });
    expect(add).toBeDisabled();
    expect(add).toHaveAccessibleDescription(`Đã đủ ${MAX_UNIT_ROWS} đơn vị, tối đa cho một hàng hóa.`);
    expect(screen.getByText(UNIT_MESSAGES.tooMany)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Xóa đơn vị đv 0" }));
    expect(add).toBeEnabled();
    expect(add).not.toHaveAccessibleDescription();
  });

  it("shows the directory's refusal of a new unit under its field", async () => {
    server.use(http.post(apiUrl("/units"), () => apiError(409, "name_taken")));
    const { user } = await renderTable({ initial: keo() });
    await user.click(screen.getByRole("button", { name: "+ Thêm đơn vị" }));
    const field = screen.getByRole("combobox", { name: "Tên đơn vị 3" });
    await user.type(field, "Lốc");
    await user.click(await screen.findByRole("option", { name: "+ Tạo mới “Lốc”" }));

    const message = "Đơn vị “Lốc” đã có trong danh mục đơn vị (có thể đang ngừng dùng).";
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Tên đơn vị 3" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("combobox", { name: "Tên đơn vị 3" })).toHaveAccessibleDescription(message);
  });

  it("marks the fields the form found wrong, each described by its error", async () => {
    await renderTable({ initial: keo(), errors: { "units.0.factor": UNIT_MESSAGES.factor, "units.0.unit": UNIT_MESSAGES.duplicate("dây") } });
    const factor = screen.getByRole("textbox", { name: "Quy đổi: 1 dây bằng bao nhiêu cái" });
    expect(factor).toHaveAttribute("aria-invalid", "true");
    expect(factor).toHaveAccessibleDescription(UNIT_MESSAGES.factor);
    expect(screen.getByRole("combobox", { name: "Tên đơn vị 2" })).toHaveAccessibleDescription(UNIT_MESSAGES.duplicate("dây"));
    expect(screen.getByRole("textbox", { name: "Giá bán lẻ / dây" })).not.toHaveAttribute("aria-invalid");
  });

  it("warns, without refusing, when a unit's own retail price is below the base unit's", async () => {
    const { user } = await renderTable({ initial: keo() });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "Giá bán lẻ / dây" }), "400");
    expect(screen.getByRole("status")).toHaveTextContent("Giá lẻ 1 dây (400 ₫) thấp hơn giá 1 cái (500 ₫)");
  });

  it("shows a base unit picked outside the table and marks the purchase-order line's unit", async () => {
    await renderTable({ initial: { ...keo(), baseUnitId: null }, baseUnitName: "cái", purchaseUnitId: DAY });
    expect(screen.queryByRole("combobox", { name: "Đơn vị cơ sở" })).not.toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Đơn vị tính & giá bán" })).getByText("cái", { selector: "b" })).toBeInTheDocument();
    expect(screen.getByText("Đơn vị nhập mặc định.")).toBeInTheDocument();
  });
});
