import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StatusPill } from "./status-pill";

describe("StatusPill", () => {
  it("reads as its text only, with the glyph hidden from screen readers", () => {
    const { container } = render(<StatusPill label="Quá hạn" tone="bad" />);
    expect(screen.getByText("Quá hạn")).toHaveClass("st", "bad");
    expect(container.querySelector(".st-g")).toHaveAttribute("aria-hidden", "true");
    expect(container.firstElementChild?.textContent).not.toBe("Quá hạn");
    expect(container.firstElementChild?.textContent?.endsWith("Quá hạn")).toBe(true);
  });
});
