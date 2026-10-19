import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { HotkeysProvider } from "../../app/providers/hotkeys-provider";
import { PENDING_LABEL } from "./button";
import { CommitDialog } from "./commit-dialog";

function renderDialog(props: { pending: boolean; pendingLabel?: string; onSubmit: () => void }) {
  return render(
    <HotkeysProvider>
      <CommitDialog open onClose={vi.fn()} title="Gửi lời mời" submitLabel="Gửi" {...props}>
        <input aria-label="Email" />
      </CommitDialog>
    </HotkeysProvider>,
  );
}

describe("CommitDialog", () => {
  it("shows the posting label on submit while a write is in flight", () => {
    renderDialog({ pending: true, onSubmit: vi.fn() });
    expect(screen.getByRole("button", { name: PENDING_LABEL })).toHaveAttribute("aria-busy", "true");
  });

  it("shows the caller's pending label and sends nothing more while it waits", async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    renderDialog({ pending: true, pendingLabel: "Đang gửi…", onSubmit });

    const busy = screen.getByRole("button", { name: "Đang gửi…" });
    expect(busy).toHaveAttribute("aria-disabled", "true");
    expect(screen.queryByRole("button", { name: PENDING_LABEL })).not.toBeInTheDocument();
    await user.click(busy);
    expect(busy).toHaveFocus();
    await user.type(screen.getByRole("textbox", { name: "Email" }), "{Enter}");

    expect(onSubmit).not.toHaveBeenCalled();
  });
});
