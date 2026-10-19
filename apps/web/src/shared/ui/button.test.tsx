import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Button, PENDING_LABEL } from "./button";

describe("Button", () => {
  it("keeps focus on a pending button and ignores presses on it", async () => {
    const onClick = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(<Button onClick={onClick}>Ghi sổ</Button>);
    await user.click(screen.getByRole("button", { name: "Ghi sổ" }));
    expect(onClick).toHaveBeenCalledTimes(1);

    rerender(
      <Button pending onClick={onClick}>
        Ghi sổ
      </Button>,
    );
    const busy = screen.getByRole("button", { name: PENDING_LABEL });
    expect(busy).toHaveFocus();
    expect(busy).toBeEnabled();
    expect(busy).toHaveAttribute("aria-disabled", "true");
    expect(busy).toHaveAttribute("aria-busy", "true");

    await user.click(busy);
    await user.keyboard("{Enter} ");
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("stays focusable while pending even when the caller also disables it", () => {
    render(
      <Button pending disabled pendingLabel="Đang gửi…">
        Gửi
      </Button>,
    );
    const busy = screen.getByRole("button", { name: "Đang gửi…" });
    expect(busy).toBeEnabled();
    expect(busy).toHaveAttribute("aria-disabled", "true");
  });

  it("does not submit its form while pending, by press or by Enter in a field", async () => {
    const onSubmit = vi.fn((e: { preventDefault: () => void }) => e.preventDefault());
    const user = userEvent.setup();
    render(
      <form onSubmit={onSubmit}>
        <input aria-label="Ghi chú" />
        <Button type="submit" pending>
          Lưu
        </Button>
      </form>,
    );

    await user.click(screen.getByRole("button", { name: PENDING_LABEL }));
    await user.type(screen.getByRole("textbox", { name: "Ghi chú" }), "a{Enter}");

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("is disabled outright when not available and nothing is in flight", async () => {
    const onClick = vi.fn();
    render(
      <Button disabled onClick={onClick}>
        Ghi sổ
      </Button>,
    );
    const button = screen.getByRole("button", { name: "Ghi sổ" });
    expect(button).toBeDisabled();
    expect(button).not.toHaveAttribute("aria-disabled");
    await userEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
  });
});
