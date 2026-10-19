import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Field } from "./field";
import { Input } from "./input";

describe("Field", () => {
  it("names the control by its label alone and describes it with the hint and the error", () => {
    render(
      <Field label="Mã giới thiệu" hint="Chữ thường, không dấu." error="Đã có người dùng.">
        <Input value="binhminh" onChange={() => {}} />
      </Field>,
    );
    const input = screen.getByRole("textbox", { name: "Mã giới thiệu" });
    expect(input).toHaveAccessibleDescription("Chữ thường, không dấu. Đã có người dùng.");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Đã có người dùng.");
    expect(input.closest("label")).not.toHaveTextContent("Đã có người dùng.");
  });

  it("describes a native control written directly inside it and keeps its own description", () => {
    render(
      <>
        <span id="own-note">Theo hóa đơn.</span>
        <Field label="Ghi chú" error="Bắt buộc.">
          <textarea aria-describedby="own-note" />
        </Field>
      </>,
    );
    const note = screen.getByRole("textbox", { name: "Ghi chú" });
    expect(note).toHaveAccessibleDescription("Theo hóa đơn. Bắt buộc.");
    expect(note).toHaveAttribute("aria-invalid", "true");
  });

  it("leaves a valid control without a description or an invalid mark", () => {
    render(
      <Field label="Tên">
        <Input value="" onChange={() => {}} />
      </Field>,
    );
    const input = screen.getByRole("textbox", { name: "Tên" });
    expect(input).not.toHaveAttribute("aria-describedby");
    expect(input).not.toHaveAttribute("aria-invalid");
  });

  it("keeps focus and typing in the control when an error appears", async () => {
    function Code() {
      const [value, setValue] = useState("");
      return (
        <Field label="Mã" error={value.length > 2 ? "Tối đa 2 ký tự." : undefined}>
          <input value={value} onChange={(e) => setValue(e.target.value)} />
        </Field>
      );
    }
    const user = userEvent.setup();
    render(<Code />);
    const input = screen.getByRole("textbox", { name: "Mã" });
    await user.type(input, "abcd");
    expect(screen.getByRole("textbox", { name: "Mã" })).toBe(input);
    expect(input).toHaveFocus();
    expect(input).toHaveValue("abcd");
    expect(input).toHaveAccessibleDescription("Tối đa 2 ký tự.");
  });
});
