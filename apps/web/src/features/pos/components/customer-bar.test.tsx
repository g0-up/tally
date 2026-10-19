import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { CustomerBar } from "./customer-bar";

const customer = (id: string, name: string, phone: string | null) => ({ id, name, phone, credit_limit: null, payment_terms_days: 15, status: "active" });

describe("CustomerBar", () => {
  it("picks the customer from a searchable list with the keyboard alone", async () => {
    const customers = [customer("cus-1", "Tạp hóa Minh Anh", "0901000001"), customer("cus-2", "Cô Lan", null)];
    // Like the server, the search answers only the customers whose name holds the typed text.
    server.use(
      http.get(apiUrl("/customers"), ({ request }) => {
        const q = (new URL(request.url).searchParams.get("q") ?? "").toLowerCase();
        return HttpResponse.json({ data: customers.filter((c) => c.name.toLowerCase().includes(q)), next_cursor: null });
      }),
    );
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerBar customer={null} onChange={onChange} priceSource={null} />);

    const picker = screen.getByRole("combobox", { name: "Đổi khách hàng" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await user.tab();
    expect(picker).toHaveFocus();
    await user.keyboard("lan");
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    expect(screen.getByRole("option", { name: /Cô Lan/ })).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{Enter}");

    expect(onChange).toHaveBeenCalledExactlyOnceWith({ id: "cus-2", name: "Cô Lan" });
  });
});
