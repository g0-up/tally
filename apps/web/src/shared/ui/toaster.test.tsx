import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { toast } from "./toast-store";
import { TOAST_DISMISS_LABEL, Toaster } from "./toaster";

describe("Toaster", () => {
  it("keeps a polite live region on the page before any toast arrives", () => {
    const { container } = render(<Toaster />);
    const polite = container.querySelector('[aria-live="polite"]');
    expect(polite).toBeInTheDocument();
    expect(polite).toBeEmptyDOMElement();
    act(() => {
      toast.success("Đã lưu phiếu.");
    });
    expect(polite).toHaveTextContent("Đã lưu phiếu.");
  });

  it("announces each error on its own, never inside another live region, until it is dismissed", async () => {
    const user = userEvent.setup();
    const { container } = render(<Toaster />);
    act(() => {
      toast.success("Đã lưu phiếu.");
      toast.commitError("Hết hàng.");
    });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Chưa ghi sổ. Hết hàng.");
    expect(alert.parentElement?.closest("[aria-live], [role=alert], [role=status]")).toBeNull();
    for (const region of container.querySelectorAll("[aria-live], [role=alert], [role=status]")) {
      expect(region.parentElement?.closest("[aria-live], [role=alert], [role=status]")).toBeNull();
    }

    await user.click(within(alert).getByRole("button", { name: TOAST_DISMISS_LABEL }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("Đã lưu phiếu.")).toBeInTheDocument();
  });
});
