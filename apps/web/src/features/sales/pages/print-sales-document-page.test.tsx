import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";

const PRINT = {
  label: "Liên 1: Giao khách",
  doc_type: "sales_document",
  title: "PHIẾU BÁN HÀNG",
  id: "sd-1",
  doc_no: "BH-0001",
  date: "2026-10-12",
  status: "posted",
  tenant: { name: "Đại lý Hòa Phát", legal_name: "Hộ kinh doanh Hòa Phát", phone: "0901234567", tax_code: null, address: "12 Lê Lợi, Huế" },
  customer: null,
  note: null,
  groups: [
    {
      mix_key: null,
      lines: [
        {
          line_no: 1,
          line_kind: "item",
          sku_code: "SP001",
          name: "Bánh quy bơ",
          variant_label: "",
          color: null,
          spec_text: null,
          unit_name: "Hộp",
          qty: "2",
          unit_price: "50000",
          discount_amount: "0",
          line_amount: "100000",
        },
      ],
    },
  ],
  totals: { goods_amount: "100000", discount_amount: "0", tax_amount: "0", total_amount: "100000" },
  payment: null,
  amounts: [],
  related: [],
  printed_at: "2026-10-12T09:00:00Z",
};

describe("print sales document page", () => {
  it("prints the server's document without the app shell and switches to the 80 mm roll", async () => {
    server.use(http.get(apiUrl("/print/sales_document/sd-1"), () => HttpResponse.json(PRINT)));
    renderApp("/print/sales-documents/sd-1");

    const doc = await screen.findByRole("article", { name: "PHIẾU BÁN HÀNG BH-0001" });
    expect(doc).toHaveClass("a5");
    expect(screen.getByText("Khách lẻ")).toBeInTheDocument();
    expect(screen.getByText("Bánh quy bơ")).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "In phiếu" })).toBeEnabled();

    await userEvent.click(screen.getByRole("button", { name: "Giấy cuộn 80mm" }));
    expect(doc).toHaveClass("roll");
    expect(screen.getByRole("button", { name: "Giấy cuộn 80mm" })).toHaveAttribute("aria-pressed", "true");
  });

  it("names the browser tab after the document and labels its header cells as column headers", async () => {
    document.title = "";
    server.use(http.get(apiUrl("/print/sales_document/sd-1"), () => HttpResponse.json(PRINT)));
    renderApp("/print/sales-documents/sd-1");

    const doc = await screen.findByRole("article", { name: "PHIẾU BÁN HÀNG BH-0001" });
    expect(document.title).toBe("PHIẾU BÁN HÀNG BH-0001 · Tally");
    const table = within(doc).getByRole("table", { name: "Dòng hàng BH-0001" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual(["STT", "Tên hàng", "ĐVT", "SL", "Đơn giá", "CK", "Thành tiền"]);
    for (const h of headers) expect(h).toHaveAttribute("scope", "col");
  });

  it("closes the print tab it was opened in", async () => {
    server.use(http.get(apiUrl("/print/sales_document/sd-1"), () => HttpResponse.json(PRINT)));
    const close = vi.spyOn(window, "close").mockImplementation(() => undefined);
    vi.spyOn(window.history, "length", "get").mockReturnValue(1);
    renderApp("/print/sales-documents/sd-1");

    await screen.findByRole("article", { name: "PHIẾU BÁN HÀNG BH-0001" });
    await userEvent.click(screen.getByRole("button", { name: "Đóng" }));
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("explains a document that cannot be loaded", async () => {
    server.use(http.get(apiUrl("/print/sales_document/missing"), () => apiError(404, "not_found", { message: "Không tìm thấy chứng từ." })));
    renderApp("/print/sales-documents/missing");

    expect(await screen.findByText(/^Không tải được chứng từ để in:/, undefined, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "In phiếu" })).toBeDisabled();
  });
});
