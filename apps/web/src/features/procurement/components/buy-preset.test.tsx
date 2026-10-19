import { useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { emptyBuyLine, withSku, type BuyLine } from "../buy-lines";
import { TEST_UNITS, UNIT_HOP, UNIT_THUNG, testSku } from "../testing/fixtures";
import { BuyEditor } from "./buy-preset";

function Editor({ initial, receive = false }: { initial: BuyLine[]; receive?: boolean }) {
  const [lines, setLines] = useState(initial);
  return <BuyEditor lines={lines} onChange={setLines} receive={receive} />;
}

function renderEditor(initial: BuyLine[], receive = false) {
  server.use(http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })));
  return renderWithProviders(<Editor initial={initial} receive={receive} />);
}

describe("BuyEditor", () => {
  it("adds an inline new product with the prototype units and names its row controls after it", async () => {
    const user = userEvent.setup();
    const { platform } = renderEditor([]);
    await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));

    await user.click(screen.getByRole("button", { name: "+ Hàng hóa mới" }));

    expect(screen.getByText("Hàng mới", { selector: ".tag" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Đơn vị cơ sở – Hàng mới" })).toHaveValue(UNIT_HOP);
    expect(screen.getByRole("combobox", { name: "Đơn vị lớn – Hàng mới" })).toHaveValue(UNIT_THUNG);
    expect(screen.getByRole("checkbox", { name: "Quản lý lô/hạn – Hàng mới" })).toBeChecked();

    await user.type(screen.getByRole("textbox", { name: "Tên hàng mới" }), "Kẹo dẻo");
    expect(screen.getByRole("textbox", { name: "Số lượng – Kẹo dẻo" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Đơn giá – Kẹo dẻo" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Xóa – Kẹo dẻo" }));
    expect(screen.queryByRole("textbox", { name: "Tên hàng mới" })).not.toBeInTheDocument();
  });

  it("turns a search that finds nothing into a new product named after the typed text", async () => {
    const user = userEvent.setup();
    server.use(http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [] })));
    const { platform } = renderEditor([emptyBuyLine()]);
    await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));

    await user.click(screen.getByRole("combobox", { name: "Tên hàng – dòng 1" }));
    await user.type(screen.getByRole("combobox", { name: "Tên hàng – dòng 1" }), "Kẹo gừng");
    await user.click(await screen.findByRole("option", { name: "+ Tạo hàng mới “Kẹo gừng”" }));

    expect(screen.getByText("Hàng mới", { selector: ".tag" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Tên hàng mới" })).toHaveValue("Kẹo gừng");
    expect(screen.getByRole("textbox", { name: "Tên hàng mới" })).toHaveFocus();
    expect(screen.getByRole("combobox", { name: "Đơn vị cơ sở – Kẹo gừng" })).toHaveValue(UNIT_HOP);
    expect(screen.queryByRole("combobox", { name: "Tên hàng – dòng 1" })).not.toBeInTheDocument();
  });

  it("asks for lot and expiry only on lot-tracked rows when receiving", async () => {
    const tracked = withSku(emptyBuyLine(), testSku({ id: "sku-lot", display_name: "Kẹo sữa", track_lot: true, track_expiry: true }));
    const plain = withSku(emptyBuyLine(), testSku());
    renderEditor([tracked, plain], true);

    expect(screen.getByRole("columnheader", { name: "Lô · hạn dùng" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Số lô – Kẹo sữa" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Số lô – Bánh quy bơ" })).not.toBeInTheDocument();
    const plainRow = screen.getByRole("textbox", { name: "Số lượng – Bánh quy bơ" }).closest("tr");
    expect(plainRow).not.toBeNull();
    expect(within(plainRow as HTMLElement).getByText("—")).toBeInTheDocument();
  });

  it("names the table and its action column, and tells unpicked rows apart by number", async () => {
    const user = userEvent.setup();
    renderEditor([withSku(emptyBuyLine(), testSku()), emptyBuyLine()]);

    const table = screen.getByRole("table", { name: "Hàng nhập" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.at(-1)).toHaveAccessibleName("Thao tác");
    expect(headers.every((h) => h.getAttribute("scope") === "col")).toBe(true);

    expect(within(table).getByRole("combobox", { name: "Tên hàng – Bánh quy bơ" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Tên hàng – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa – Bánh quy bơ" })).toBeInTheDocument();

    await user.click(within(table).getByRole("button", { name: "Xóa – dòng 2" }));
    expect(within(table).queryByRole("textbox", { name: "Số lượng – dòng 2" })).not.toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – Bánh quy bơ" })).toBeInTheDocument();
  });

  it("has no lot column on an order", () => {
    renderEditor([withSku(emptyBuyLine(), testSku({ track_lot: true }))]);
    expect(screen.queryByRole("columnheader", { name: "Lô · hạn dùng" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /^Số lô/ })).not.toBeInTheDocument();
  });
});
