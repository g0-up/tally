import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ApiError } from "../../../shared/api/api-error";
import { ShiftFallbackCallout } from "./shift-fallback-callout";

describe("ShiftFallbackCallout", () => {
  it("alerts that no shift is open when the server refused the sale for it", () => {
    render(<ShiftFallbackCallout error={new ApiError({ status: 409, code: "shift_unavailable", message: "No open shift" })} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Chưa có ca bán hàng đang mở.");
  });

  it("stays out of the way for any other outcome, since the first sale opens the shift by itself", () => {
    const { container, rerender } = render(<ShiftFallbackCallout error={null} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<ShiftFallbackCallout error={new ApiError({ status: 422, code: "validation_failed", message: "Bad line" })} />);
    expect(container).toBeEmptyDOMElement();
  });
});
