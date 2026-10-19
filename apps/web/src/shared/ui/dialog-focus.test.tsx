import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { HotkeysProvider } from "../../app/providers/hotkeys-provider";
import { CommitDialog } from "./commit-dialog";
import { DetailDrawer } from "./detail-drawer";

function DialogOpener() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Mở hộp thoại
      </button>
      <CommitDialog open={open} onClose={() => setOpen(false)} title="Hộp thoại" onSubmit={() => setOpen(false)}>
        <input aria-label="Ghi chú" />
      </CommitDialog>
    </>
  );
}

function DrawerOpener() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Xem chi tiết
      </button>
      <DetailDrawer open={open} onClose={() => setOpen(false)} title="Chi tiết">
        <p>Nội dung</p>
      </DetailDrawer>
    </>
  );
}

describe("dialog focus", () => {
  it.each([
    ["commit dialog", <DialogOpener key="d" />, "Mở hộp thoại"],
    ["detail drawer", <DrawerOpener key="r" />, "Xem chi tiết"],
  ])("%s returns focus to the button that opened it on Esc", async (_name, ui, opener) => {
    const user = userEvent.setup();
    render(<HotkeysProvider>{ui}</HotkeysProvider>);
    const button = screen.getByRole("button", { name: opener });
    await user.click(button);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(button).not.toHaveFocus();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(button).toHaveFocus());
  });
});

/** A row whose "Xóa" button opens a dialog that deletes the row, and with it the opener. */
function DeletableRow({ withFallback }: { withFallback: boolean }) {
  const [open, setOpen] = useState(false);
  const [deleted, setDeleted] = useState(false);
  return (
    <main>
      <h2 tabIndex={-1} id="list-title">
        Danh sách
      </h2>
      {deleted ? null : (
        <button type="button" onClick={() => setOpen(true)}>
          Xóa dòng
        </button>
      )}
      <CommitDialog
        open={open}
        onClose={() => setOpen(false)}
        title="Xóa dòng?"
        onSubmit={() => {
          setDeleted(true);
          setOpen(false);
        }}
        submitLabel="Xóa"
        {...(withFallback ? { returnFocusFallback: () => document.getElementById("list-title") } : {})}
      >
        <p>Không hoàn tác được.</p>
      </CommitDialog>
    </main>
  );
}

describe("dialog focus when the opener is gone", () => {
  it.each([
    ["the caller's fallback", true, () => screen.getByRole("heading", { name: "Danh sách" })],
    ["the page's main region", false, () => screen.getByRole("main")],
  ])("moves focus to %s", async (_name, withFallback, target) => {
    const user = userEvent.setup();
    render(
      <HotkeysProvider>
        <DeletableRow withFallback={withFallback} />
      </HotkeysProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Xóa dòng" }));
    const dialog = await screen.findByRole("dialog", { name: "Xóa dòng?" });
    await user.click(within(dialog).getByRole("button", { name: "Xóa" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Xóa dòng" })).not.toBeInTheDocument();
    await waitFor(() => expect(target()).toHaveFocus());
    expect(document.body).not.toHaveFocus();
  });
});

describe("commit dialog summary", () => {
  it("keeps the summary in a polite live region that is there before the summary is", async () => {
    function Summary() {
      const [qty, setQty] = useState("");
      return (
        <CommitDialog open onClose={() => {}} title="Nhập hàng" live={qty ? <p>Tổng {qty} hộp</p> : null}>
          <input aria-label="Số lượng" value={qty} onChange={(e) => setQty(e.target.value)} />
        </CommitDialog>
      );
    }
    const user = userEvent.setup();
    render(
      <HotkeysProvider>
        <Summary />
      </HotkeysProvider>,
    );
    const dialog = await screen.findByRole("dialog", { name: "Nhập hàng" });
    const region = dialog.querySelector('[aria-live="polite"]');
    expect(region).toBeInTheDocument();
    expect(region).toBeEmptyDOMElement();
    await user.type(within(dialog).getByRole("textbox", { name: "Số lượng" }), "12");
    expect(region).toHaveTextContent("Tổng 12 hộp");
    expect(within(dialog).getByRole("textbox", { name: "Số lượng" })).toHaveFocus();
  });
});
