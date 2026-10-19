import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { mixGroupHandlers, testMixGroup } from "../testing/fixtures";

const PATH = "/catalog/mix-groups";
const STOPPED = testMixGroup({ id: "mix-2", name: "Nước ngọt mix", status: "inactive", usage: [], used_sku_ids: [] });
const STOPPED_TOO = testMixGroup({ id: "mix-3", name: "Bánh mix", status: "inactive", usage: [], used_sku_ids: [] });

function held() {
  let release!: () => void;
  const answer = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { answer, release };
}

describe("MixGroupsPage", () => {
  it("asks before stopping a group and sends nothing until it is confirmed", async () => {
    const stopped: string[] = [];
    server.use(
      ...mixGroupHandlers([testMixGroup()]),
      http.post(/\/mix-groups\/mix-1:deactivate$/, ({ request }) => {
        stopped.push(request.url);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhóm mix vị" });
    const user = userEvent.setup();
    await user.click(within(table).getByRole("button", { name: "Ngừng dùng Kẹo mix vị" }));

    const dialog = await screen.findByRole("dialog", { name: "Ngừng dùng nhóm mix Kẹo mix vị?" });
    expect(dialog).toHaveTextContent("Chứng từ cũ vẫn giữ nguyên.");
    expect(stopped).toHaveLength(0);

    await user.click(within(dialog).getByRole("button", { name: "Ngừng dùng" }));
    expect(await screen.findByText("Đã ngừng dùng nhóm mix. Chứng từ cũ vẫn giữ nguyên.")).toBeInTheDocument();
    expect(stopped).toHaveLength(1);
  });

  it("names each row's buttons after its group, in the touch-sized actions cell", async () => {
    server.use(...mixGroupHandlers([testMixGroup(), STOPPED]));
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhóm mix vị" });
    const used = within(table).getByRole("row", { name: /Kẹo mix vị/ });
    expect(within(used).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["Sửa Kẹo mix vị", "Ngừng dùng Kẹo mix vị"]);
    const unused = within(table).getByRole("row", { name: /Nước ngọt mix/ });
    expect(within(unused).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["Sửa Nước ngọt mix", "Dùng lại Nước ngọt mix", "Xóa Nước ngọt mix"]);
    expect(within(unused).getByRole("button", { name: "Xóa Nước ngọt mix" }).closest("td")).toHaveClass("acts");
  });

  it("shows the wait only on the group being reused and holds the other rows until it is stored", async () => {
    const reply = held();
    server.use(
      ...mixGroupHandlers([STOPPED, STOPPED_TOO]),
      http.post(/\/mix-groups\/mix-2:reactivate$/, async () => {
        await reply.answer;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhóm mix vị" });
    await userEvent.setup().click(within(table).getByRole("button", { name: "Dùng lại Nước ngọt mix" }));

    const running = within(table).getByRole("button", { name: "Dùng lại Nước ngọt mix" });
    await waitFor(() => expect(running).toHaveAttribute("aria-busy", "true"));
    const other = within(table).getByRole("button", { name: "Dùng lại Bánh mix" });
    expect(other).toBeDisabled();
    expect(other).not.toHaveAttribute("aria-busy");
    expect(screen.queryByText("Đã dùng lại nhóm mix.")).not.toBeInTheDocument();

    reply.release();
    expect(await screen.findByText("Đã dùng lại nhóm mix.")).toBeInTheDocument();
  });
});
