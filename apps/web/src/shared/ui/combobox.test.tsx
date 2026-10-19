import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../test/render";
import { Combobox, COMBO_MORE_HINT, type ComboItem } from "./combobox";
import { COMBO_MAX_RESULTS } from "./combobox-pools";

const ITEMS: ComboItem[] = [
  { id: "SP001", name: "Bánh quy bơ Danisa 454g", inputLabel: "SP001 · Bánh quy bơ Danisa 454g" },
  { id: "SP002", name: "Kẹo dẻo Haribo", inputLabel: "SP002 · Kẹo dẻo Haribo", keywords: ["8934563123456"] },
  { id: "SP003", name: "Mứt gừng Đà Lạt", inputLabel: "SP003 · Mứt gừng Đà Lạt" },
];

function Harness({ items = ITEMS, onPick }: { items?: ComboItem[]; onPick?: (item: ComboItem) => void }) {
  const [value, setValue] = useState<string | null>(null);
  return (
    <>
      <Combobox
        items={items}
        value={value}
        onChange={(item) => {
          setValue(item.id);
          onPick?.(item);
        }}
      />
      <button type="button">Next field</button>
    </>
  );
}

const input = () => screen.getByRole("combobox", { name: "Tìm và chọn hàng hóa" });

describe("Combobox", () => {
  it("finds accented names from unaccented text", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "banh");
    const options = within(screen.getByRole("listbox")).getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent("Bánh quy bơ Danisa 454g");
  });

  it("searches hidden keywords such as barcodes", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "8934563");
    expect(within(screen.getByRole("listbox")).getByRole("option")).toHaveTextContent("Kẹo dẻo Haribo");
  });

  it("moves with the arrow keys, wraps and picks with Enter", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<Harness onPick={onPick} />);
    await user.click(await screen.findByRole("combobox"));
    expect(screen.getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(screen.getAllByRole("option")[2]).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowDown}");
    expect(screen.getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowUp}{Enter}");
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: "SP003" }));
    expect(input()).toHaveValue("SP003 · Mứt gừng Đà Lạt");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("closes on Esc and restores the chosen label", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(await screen.findByRole("combobox"));
    await user.keyboard("{Enter}");
    await user.click(input());
    await user.type(input(), "keo");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(input()).toHaveValue("SP001 · Bánh quy bơ Danisa 454g");
  });

  it("picks the only match on Tab", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<Harness onPick={onPick} />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "mut gung");
    await user.tab();
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: "SP003" }));
    expect(screen.getByRole("button", { name: "Next field" })).toHaveFocus();
  });

  it("does not pick on Tab when several options match", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<Harness onPick={onPick} />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "sp00");
    await user.tab();
    expect(onPick).not.toHaveBeenCalled();
  });

  it("caps the list at 50 and asks for more text", async () => {
    const many = Array.from({ length: 120 }, (_, i) => ({ id: `P${i}`, name: `Hộp quà số ${i}` }));
    const user = userEvent.setup();
    renderWithProviders(<Harness items={many} />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "hop qua");
    expect(screen.getAllByRole("option")).toHaveLength(COMBO_MAX_RESULTS);
    expect(screen.getByText(COMBO_MORE_HINT)).toBeInTheDocument();
  });

  it("keeps the first typed character when typing starts right after focus", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(await screen.findByRole("combobox"));
    await user.keyboard("banh");
    expect(input()).toHaveValue("banh");
  });

  it("quotes the text when nothing matches", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), "xyz");
    expect(screen.getByText("Không tìm thấy “xyz”.")).toBeInTheDocument();
  });

  it("offers the typed text for creation after the matches, reachable by keyboard", async () => {
    const onCreate = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<Combobox items={ITEMS} value={null} onChange={() => undefined} onCreate={onCreate} createLabel={(t) => `+ Tạo hàng mới “${t}”`} />);
    await user.click(await screen.findByRole("combobox"));
    await user.type(input(), " Kẹo gừng ");
    expect(screen.getByText("Không tìm thấy “ Kẹo gừng ”.")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "+ Tạo hàng mới “Kẹo gừng”" })).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{Enter}");
    expect(onCreate).toHaveBeenCalledWith("Kẹo gừng");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    await user.clear(input());
    await user.type(input(), "keo");
    const options = screen.getAllByRole("option");
    expect(options.at(-1)).toHaveAccessibleName("+ Tạo hàng mới “keo”");
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowUp}{Enter}");
    expect(onCreate).toHaveBeenLastCalledWith("keo");
  });

  it("offers no creation without onCreate or typed text", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Combobox items={ITEMS} value={null} onChange={() => undefined} onCreate={() => undefined} />);
    await user.click(await screen.findByRole("combobox"));
    expect(screen.queryByRole("option", { name: /Tạo mới/ })).not.toBeInTheDocument();
  });
});
