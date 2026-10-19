import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { PriceHistoryTable } from "./price-history-table";

describe("PriceHistoryTable", () => {
  it("names a failed read of the history with a retry, never as a history with no changes", async () => {
    let failing = true;
    server.use(
      http.get(apiUrl("/price-changes"), () => (failing ? apiError(500, "internal") : HttpResponse.json({ data: [], next_cursor: null }))),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    );
    renderWithProviders(<PriceHistoryTable skuId={null} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/^Không tải được lịch sử giá: \S/);
    expect(screen.queryByText("Chưa có thay đổi giá.")).not.toBeInTheDocument();

    failing = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Thử lại" }));
    expect(await screen.findByText("Chưa có thay đổi giá.")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
