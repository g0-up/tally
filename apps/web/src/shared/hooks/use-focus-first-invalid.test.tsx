import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { useFocusFirstInvalid } from "./use-focus-first-invalid";

/** A field outside any dialog, marked invalid from the start. */
function Page() {
  return <input aria-label="Ô ngoài" aria-invalid="true" readOnly />;
}

/** A dialog whose check marks its second field, then asks for focus on the first marked one. */
function CheckedDialog() {
  const [marked, setMarked] = useState(false);
  const focusFirstInvalid = useFocusFirstInvalid();
  return (
    <div role="dialog" aria-label="Hộp kiểm tra">
      <input aria-label="Tên" />
      <input aria-label="Số điện thoại" aria-invalid={marked || undefined} />
      <button
        type="button"
        onClick={() => {
          setMarked(true);
          focusFirstInvalid();
        }}
      >
        Lưu
      </button>
    </div>
  );
}

/** A check that runs outside any dialog. */
function PageCheck() {
  const focusFirstInvalid = useFocusFirstInvalid();
  return (
    <button type="button" onClick={focusFirstInvalid}>
      Kiểm tra
    </button>
  );
}

describe("useFocusFirstInvalid", () => {
  it("focuses the first marked input of the dialog the user is in, once the mark is drawn", async () => {
    const user = userEvent.setup();
    render(
      <>
        <Page />
        <CheckedDialog />
      </>,
    );

    await user.click(screen.getByRole("button", { name: "Lưu" }));

    expect(screen.getByRole("textbox", { name: "Số điện thoại" })).toHaveFocus();
  });

  it("looks in the whole page when the user is not in a dialog", async () => {
    const user = userEvent.setup();
    render(
      <>
        <Page />
        <PageCheck />
      </>,
    );

    await user.click(screen.getByRole("button", { name: "Kiểm tra" }));

    expect(screen.getByRole("textbox", { name: "Ô ngoài" })).toHaveFocus();
  });
});
