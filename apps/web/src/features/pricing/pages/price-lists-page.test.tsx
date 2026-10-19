import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { priceListSchema } from "../schemas";

const PATH = "/catalog/price-lists";
const RETAIL = priceListSchema.parse({ id: "pl-retail", name: "Giá lẻ", kind: "retail", customer_group_id: null, party_id: null, campaign_id: null, priority: 0, valid_from: null, valid_to: null, active: true, item_count: 0 });
const empty = () => HttpResponse.json({ data: [], next_cursor: null });

describe("PriceListsPage", () => {
  it("names a failed read of the lists with a retry instead of an empty table", async () => {
    let failing = true;
    server.use(
      http.get(apiUrl("/price-lists"), () => (failing ? apiError(500, "internal") : HttpResponse.json({ data: [RETAIL], next_cursor: null }))),
      http.get(apiUrl("/customer-groups"), empty),
      http.get(apiUrl("/skus"), empty),
    );
    renderApp(PATH);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/^Không tải được bảng giá: \S/);
    expect(screen.queryByRole("table", { name: "Bảng giá" })).not.toBeInTheDocument();

    failing = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Thử lại" }));
    const table = await screen.findByRole("table", { name: "Bảng giá" });
    expect(within(table).getByRole("row", { name: /Giá lẻ/ })).toBeInTheDocument();
    expect(screen.queryByText(/^Không tải được bảng giá/)).not.toBeInTheDocument();
  });

  it("says the customer groups could not be read instead of leaving the scopes blank", async () => {
    server.use(
      http.get(apiUrl("/price-lists"), () => HttpResponse.json({ data: [RETAIL], next_cursor: null })),
      http.get(apiUrl("/customer-groups"), () => apiError(500, "internal")),
      http.get(apiUrl("/skus"), empty),
    );
    renderApp(PATH);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/^Không tải được nhóm khách: \S/);
    expect(within(alert).getByRole("button", { name: "Thử lại" })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Bảng giá" })).toBeInTheDocument();
  });
});
