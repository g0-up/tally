import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { QuoteStatus } from "../schemas";
import { QuoteActions } from "./quote-actions";

function buttonNames(): string[] {
  return screen.getAllByRole("button").map((b) => b.textContent ?? "");
}

describe("QuoteActions", () => {
  it.each<[QuoteStatus, string[]]>([
    ["draft", ["Gửi khách", "Sửa", "Xóa", "Sao chép"]],
    ["sent", ["Khách chấp nhận", "Khách từ chối", "Phiên bản mới", "Sao chép"]],
    ["accepted", ["Chuyển thành hóa đơn", "Sao chép"]],
    ["rejected", ["Phiên bản mới", "Sao chép"]],
    ["expired", ["Gia hạn / phiên bản mới", "Khách từ chối", "Sao chép"]],
    ["converted", ["Sao chép"]],
  ])("a %s quote offers %j", (status, names) => {
    render(<QuoteActions status={status} onAction={vi.fn()} />);
    expect(buttonNames()).toEqual(names);
  });

  it("shows the invoice a converted quote became", () => {
    const { container } = render(<QuoteActions status="converted" invoiceDocNo="SO-0042" onAction={vi.fn()} />);
    expect(within(container).getByText("SO-0042")).toBeInTheDocument();
  });

  it("reports the action clicked", async () => {
    const onAction = vi.fn();
    render(<QuoteActions status="sent" onAction={onAction} />);
    await userEvent.click(screen.getByRole("button", { name: "Khách chấp nhận" }));
    expect(onAction).toHaveBeenCalledWith("accept");
  });

  it("blocks every action while one is in flight, keeping the running one focusable", async () => {
    const onAction = vi.fn();
    render(<QuoteActions status="draft" pending="send" onAction={onAction} />);
    const running = screen.getByRole("button", { name: "Đang ghi sổ…" });
    expect(running).toHaveAttribute("aria-busy", "true");
    expect(running).toHaveAttribute("aria-disabled", "true");
    for (const b of screen.getAllByRole("button")) if (b !== running) expect(b).toBeDisabled();
    await userEvent.click(running);
    expect(onAction).not.toHaveBeenCalled();
  });
});
