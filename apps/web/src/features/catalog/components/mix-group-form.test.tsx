import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { mixGroupHandlers, testMixGroup } from "../testing/fixtures";

const LOCK_REASON = /Nhóm đã dùng trên chứng từ nên không đổi được đơn vị và quy cách thùng\./;

async function openEdit(name: string) {
  renderApp("/catalog/mix-groups");
  const table = await screen.findByRole("table", { name: "Nhóm mix vị" });
  const user = userEvent.setup();
  await user.click(within(table).getByRole("button", { name: `Sửa ${name}` }));
  return { dialog: await screen.findByRole("dialog", { name: "Sửa nhóm mix vị" }), user };
}

describe("MixGroupFormFields", () => {
  it("keeps the fixed spec of a used group read-only, reachable by keyboard and saying why", async () => {
    server.use(...mixGroupHandlers([testMixGroup()]));
    const { dialog, user } = await openEdit("Kẹo mix vị");

    const packUnit = await within(dialog).findByRole("textbox", { name: "Đơn vị thùng" });
    expect(packUnit).toHaveValue("thùng");
    const packSize = within(dialog).getByRole("textbox", { name: /^Số đơn vị lẻ \/ thùng/ });
    expect(packSize).toHaveValue("12");
    const partUnit = within(dialog).getByRole("textbox", { name: "Đơn vị chọn vị" });
    const partSize = within(dialog).getByRole("textbox", { name: /^Số đơn vị lẻ \/ đơn vị chọn vị/ });
    for (const fixed of [packUnit, packSize, partUnit, partSize]) {
      expect(fixed).toHaveAttribute("readonly");
      expect(fixed).not.toBeDisabled();
      expect(fixed).toHaveAccessibleDescription(LOCK_REASON);
    }
    expect(within(dialog).queryByRole("combobox", { name: "Đơn vị thùng" })).not.toBeInTheDocument();

    await user.click(packSize);
    expect(packSize).toHaveFocus();
    await user.type(packSize, "4");
    expect(packSize).toHaveValue("12");
  });

  it("names the flavor table and each line's inputs and buttons after the flavor", async () => {
    server.use(...mixGroupHandlers([testMixGroup()]));
    const { dialog } = await openEdit("Kẹo mix vị");

    const table = await within(dialog).findByRole("table", { name: "Các vị trong nhóm" });
    expect(within(table).getAllByRole("columnheader").at(-1)).toHaveTextContent("Thao tác");
    expect(within(table).getByRole("combobox", { name: "Mặt hàng vị 1" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Mặt hàng vị 2" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Tên vị ngắn KD01" })).toHaveValue("Dâu");
    expect(within(table).getByRole("textbox", { name: "Tên vị ngắn KC01" })).toHaveValue("Cam");
    // The flavor already sold cannot be removed; the other one names itself.
    expect(within(table).getAllByRole("button", { name: /^Gỡ vị / }).map((b) => b.getAttribute("aria-label"))).toEqual(["Gỡ vị Kẹo cam"]);
    expect(within(table).getByText("đã bán").closest("td")).toHaveClass("acts");
  });

  it("lets the spec of a group no document uses be picked again", async () => {
    server.use(...mixGroupHandlers([testMixGroup({ usage: [], used_sku_ids: [] })]));
    const { dialog } = await openEdit("Kẹo mix vị");

    expect(await within(dialog).findByRole("combobox", { name: "Đơn vị thùng" })).not.toBeDisabled();
    const packSize = within(dialog).getByRole("textbox", { name: /^Số đơn vị lẻ \/ thùng/ });
    expect(packSize).not.toHaveAttribute("readonly");
    expect(packSize).not.toHaveAccessibleDescription(LOCK_REASON);
  });
});
