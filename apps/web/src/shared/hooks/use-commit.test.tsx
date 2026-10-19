import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { ApiError, ERROR_CODES } from "../api/api-error";
import { PATHS } from "../api/endpoints";
import { apiError, apiUrl } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { renderWithProviders } from "../../test/render";
import { Button, PENDING_LABEL } from "../ui/button";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../ui/reauth-dialog";
import { COMMIT_FAILED } from "../ui/toast-store";
import { useCommit, type CommitContext } from "./use-commit";

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function Form({ send, after }: { send: (ctx: CommitContext) => Promise<{ doc_no: string }>; after?: () => void }) {
  const commit = useCommit<{ doc_no: string }>({
    mutationFn: (_v, ctx) => send(ctx),
    success: (data) => (
      <>
        Đã ghi sổ <b>{data.doc_no}</b>.
      </>
    ),
    ...(after ? { after } : {}),
  });
  return (
    <Button variant="primary" pending={commit.pending} onClick={() => void commit.run()}>
      Ghi sổ
    </Button>
  );
}

describe("useCommit", () => {
  it("shows the pending label and announces success only after the server answered", async () => {
    const reply = deferred<{ doc_no: string }>();
    const send = vi.fn(() => reply.promise);
    const after = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<Form send={send} after={after} />);
    await user.click(await screen.findByRole("button", { name: "Ghi sổ" }));
    const busy = await screen.findByRole("button", { name: PENDING_LABEL });
    // It keeps focus while the server answers; pressing it again sends nothing.
    expect(busy).toHaveAttribute("aria-disabled", "true");
    expect(busy).toHaveFocus();
    await user.click(busy);
    expect(send).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/Đã ghi sổ/)).not.toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();
    reply.resolve({ doc_no: "HD-0001" });
    expect(await screen.findByText("HD-0001")).toBeInTheDocument();
    expect(after).toHaveBeenCalledTimes(1);
    expect(await screen.findByRole("button", { name: "Ghi sổ" })).toBeEnabled();
  });

  it("sends one request for a double click", async () => {
    const reply = deferred<{ doc_no: string }>();
    const send = vi.fn(() => reply.promise);
    renderWithProviders(<Form send={send} />);
    const button = await screen.findByRole("button", { name: "Ghi sổ" });
    button.click();
    button.click();
    reply.resolve({ doc_no: "HD-0002" });
    await screen.findByText("HD-0002");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("keeps a persistent error toast and reuses the key when the same intent is retried", async () => {
    const keys: string[] = [];
    const send = vi
      .fn<(ctx: CommitContext) => Promise<{ doc_no: string }>>()
      .mockImplementationOnce(async (ctx) => {
        keys.push(ctx.idempotencyKey);
        throw new ApiError({ status: 422, code: "insufficient_stock", message: "Không đủ tồn kho." });
      })
      .mockImplementationOnce(async (ctx) => {
        keys.push(ctx.idempotencyKey);
        return { doc_no: "HD-0003" };
      });
    const user = userEvent.setup();
    renderWithProviders(<Form send={send} />);
    await user.click(await screen.findByRole("button", { name: "Ghi sổ" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(COMMIT_FAILED);
    expect(alert).toHaveTextContent("Không đủ tồn kho.");
    expect(screen.getByRole("button", { name: "Đóng thông báo" })).toBeInTheDocument();
    expect(screen.queryByText(/Đã ghi sổ/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Ghi sổ" }));
    expect(await screen.findByText("HD-0003")).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
    // The error toast stays until dismissed.
    expect(screen.getByRole("alert")).toHaveTextContent("Không đủ tồn kho.");
  });

  it("asks for the password on reauth_required and retries the same intent", async () => {
    const keys: string[] = [];
    const send = vi
      .fn<(ctx: CommitContext) => Promise<{ doc_no: string }>>()
      .mockImplementationOnce(async (ctx) => {
        keys.push(ctx.idempotencyKey);
        throw new ApiError({ status: 403, code: "reauth_required", message: "reauth" });
      })
      .mockImplementationOnce(async (ctx) => {
        keys.push(ctx.idempotencyKey);
        return { doc_no: "HD-0004" };
      });
    const { server } = await import("../../test/msw/server");
    const { http, HttpResponse } = await import("msw");
    server.use(http.post("/api/v1/auth/reauth", () => new HttpResponse(null, { status: 204 })));
    const user = userEvent.setup();
    renderWithProviders(<Form send={send} />);
    await user.click(await screen.findByRole("button", { name: "Ghi sổ" }));
    await user.type(await screen.findByLabelText("Mật khẩu"), "secret");
    await user.click(screen.getByRole("button", { name: "Xác nhận" }));
    expect(await screen.findByText("HD-0004")).toBeInTheDocument();
    expect(keys[0]).toBe(keys[1]);
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("asks for the password on reauth_required and retries the same intent once", async () => {
    const reauthBodies: unknown[] = [];
    server.use(
      http.post(apiUrl(PATHS.reauth), async ({ request }) => {
        const body = (await request.json()) as { password?: string };
        reauthBodies.push(body);
        return body.password === "dung-mat-khau" ? HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" }) : apiError(403, "invalid_password");
      }),
    );
    const keys: string[] = [];
    const send = vi.fn((ctx: CommitContext) => {
      keys.push(ctx.idempotencyKey);
      return keys.length === 1 ? Promise.reject(new ApiError({ status: 403, code: ERROR_CODES.reauthRequired, message: "reauth" })) : Promise.resolve({ doc_no: "HD-0005" });
    });
    const user = userEvent.setup();
    renderWithProviders(<Form send={send} />);
    await user.click(await screen.findByRole("button", { name: "Ghi sổ" }));

    const dialog = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    const password = within(dialog).getByLabelText("Mật khẩu");
    await user.type(password, "sai");
    await user.click(within(dialog).getByRole("button", { name: REAUTH_SUBMIT }));
    expect(await within(dialog).findByText("Mật khẩu không đúng.")).toBeInTheDocument();
    expect(send).toHaveBeenCalledTimes(1);

    await user.clear(password);
    await user.type(password, "dung-mat-khau");
    await user.click(within(dialog).getByRole("button", { name: REAUTH_SUBMIT }));

    expect(await screen.findByText("HD-0005")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: REAUTH_TITLE })).not.toBeInTheDocument();
    expect(reauthBodies).toEqual([{ password: "sai" }, { password: "dung-mat-khau" }]);
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });
});
