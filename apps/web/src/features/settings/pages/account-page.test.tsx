import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { READ_ONLY_REASON } from "../../../shared/hooks/use-read-only";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl, withEntitlements } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { CLOSURE_STATUSES, type ClosureStatus } from "../api/account";
import { CLOSURE_CANCEL_LABEL, CLOSURE_CANCELLED, CLOSURE_REQUEST_LABEL, CLOSURE_RESEND_LABEL } from "../components/account-closure-card";
import { DATA_EXPORT_QUEUED } from "../components/data-export-card";
import { CHANGE_PASSWORD_LABEL } from "../components/change-password-dialog";
import { DEVICE_SIGNED_OUT, REVOKE_OTHERS_LABEL } from "../components/devices-card";
import {
  MFA_ENABLED_TOAST,
  MFA_SETUP_TITLE,
  RECOVERY_CODES_COPIED,
  RECOVERY_CODES_FILE,
  RECOVERY_CODES_SAVED,
  RECOVERY_CODES_TITLE,
  RECOVERY_CODES_UNSAVED,
} from "../components/mfa-setup-dialog";
import { OWNER_TRANSFER_CONFIRM_TITLE, OWNER_TRANSFER_SUBMIT } from "../components/owner-transfer-card";
import { LOCK_CONFIRM_TITLE, LOCK_LABEL, REOPEN_EXCEPTION_CHECKBOX, REOPEN_EXCEPTION_TITLE, REOPEN_LABEL } from "../components/period-lock-card";
import { STORE_PROFILE_SAVED } from "../components/store-profile-card";
import {
  LOCK_EVENT,
  NO_CLOSURE,
  OTHER_SESSION,
  accountHandlers,
  testClosureRequest,
  testExportJob,
  testOwner,
  testPeriodLocks,
  testStoreProfile,
} from "../testing/fixtures";
import { ACCOUNT_LEAD } from "./account-page";

const PATH = "/settings/account";
const QUEUED_EXPORT = { id: "exp-1", status: "queued", created_at: "2026-10-12T02:00:00Z" };
const CONFIRM_PASSWORD = "Xác nhận mật khẩu";

const cardOf = (title: string) => screen.getByRole("heading", { name: title }).closest(".card") as HTMLElement;
const findCard = async (title: string) => (await screen.findByRole("heading", { name: title })).closest(".card") as HTMLElement;

/** Answers POST /auth/reauth and counts the calls. */
function reauthHandler() {
  const calls: unknown[] = [];
  const handler = http.post(apiUrl(PATHS.reauth), async ({ request }) => {
    calls.push(await request.json());
    return HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" });
  });
  return { handler, calls };
}

async function enterPassword(user: UserEvent) {
  const dialog = await screen.findByRole("dialog", { name: REAUTH_TITLE });
  await user.type(within(dialog).getByLabelText("Mật khẩu"), "mat-khau");
  await user.click(within(dialog).getByRole("button", { name: REAUTH_SUBMIT }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: REAUTH_TITLE })).not.toBeInTheDocument());
}

/** Opens a section kept behind RequireRecentAuth. */
async function unlock(user: UserEvent, card: HTMLElement) {
  await user.click(within(card).getByRole("button", { name: CONFIRM_PASSWORD }));
  await enterPassword(user);
}

describe("AccountPage", () => {
  it("shows the owner, devices, store profile, period lock and the offline policy", async () => {
    server.use(...accountHandlers());
    renderApp(PATH);

    expect(await screen.findByText(ACCOUNT_LEAD)).toBeInTheDocument();
    const owner = await findCard("Chủ cửa hàng");
    expect(await within(owner).findByText("Nguyễn Văn An")).toBeInTheDocument();
    expect(within(owner).getByText("Chưa bật")).toBeInTheDocument();

    const devices = cardOf("Thiết bị đang đăng nhập");
    expect(await within(devices).findByText("Thiết bị này")).toBeInTheDocument();
    expect(within(devices).getByRole("button", { name: `Đăng xuất ${OTHER_SESSION.device_label}` })).toBeInTheDocument();

    expect(await screen.findByRole("textbox", { name: /^Tên pháp lý/ })).toHaveValue("Hộ kinh doanh Hòa Phát");
    expect(screen.getByRole("textbox", { name: "Ngày chốt số dư đầu kỳ" })).toHaveValue("30/09/2026");
    expect(screen.getByText("Đang khóa đến hết ngày 30/09/2026.")).toBeInTheDocument();
    expect(cardOf("Khóa sổ kỳ")).toHaveTextContent("Khóa sổ");
    expect(await screen.findByRole("checkbox", { name: "Bán ngoại tuyến tại quầy" })).toBeInTheDocument();
    expect(cardOf("Đóng tài khoản")).toHaveTextContent("Đóng tài khoản cần xác nhận lại mật khẩu.");
  });

  it("signs out another device", async () => {
    const deleted: string[] = [];
    server.use(
      ...accountHandlers(),
      http.delete(apiUrl("/sessions/:id"), ({ params }) => {
        deleted.push(String(params.id));
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: `Đăng xuất ${OTHER_SESSION.device_label}` }));

    expect(await screen.findByText(DEVICE_SIGNED_OUT)).toBeInTheDocument();
    expect(deleted).toEqual([OTHER_SESSION.id]);
  });

  it("names the refusal in the error toast and shows no success", async () => {
    server.use(
      ...accountHandlers(),
      http.delete(apiUrl("/sessions/:id"), () => apiError(404, "not_found")),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: `Đăng xuất ${OTHER_SESSION.device_label}` }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Chưa ghi sổ.");
    expect(toast).toHaveTextContent("Không tìm thấy dữ liệu, có thể đã bị xóa.");
    expect(screen.queryByText(DEVICE_SIGNED_OUT)).not.toBeInTheDocument();
  });

  it("signs out every other device after a confirmation", async () => {
    let calls = 0;
    server.use(
      ...accountHandlers(),
      http.post(apiUrl("/sessions:revoke-all"), () => {
        calls += 1;
        return HttpResponse.json({ revoked: 1 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: REVOKE_OTHERS_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: `${REVOKE_OTHERS_LABEL}?` });
    expect(calls).toBe(0);
    await user.click(within(dialog).getByRole("button", { name: REVOKE_OTHERS_LABEL }));

    expect(await screen.findByText("Đã đăng xuất 1 thiết bị khác.")).toBeInTheDocument();
    expect(calls).toBe(1);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `${REVOKE_OTHERS_LABEL}?` })).not.toBeInTheDocument());
  });

  it("keeps the confirmation open and shows no success when signing out the other devices is refused", async () => {
    server.use(
      ...accountHandlers(),
      http.post(apiUrl("/sessions:revoke-all"), () => apiError(409, "sandbox_replaced")),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: REVOKE_OTHERS_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: `${REVOKE_OTHERS_LABEL}?` });
    await user.click(within(dialog).getByRole("button", { name: REVOKE_OTHERS_LABEL }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Chưa ghi sổ.");
    expect(screen.getByRole("dialog", { name: `${REVOKE_OTHERS_LABEL}?` })).toBeInTheDocument();
    expect(screen.queryByText(/thiết bị khác\.$/)).not.toBeInTheDocument();
  });

  it("changes the password after the password prompt, retrying the same request", async () => {
    const reauth = reauthHandler();
    const sent: unknown[] = [];
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/auth/password"), async ({ request }) => {
        sent.push(await request.json());
        return reauth.calls.length === 0 ? apiError(403, "reauth_required") : HttpResponse.json({ revoked: 2 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: CHANGE_PASSWORD_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: CHANGE_PASSWORD_LABEL });
    await user.type(within(dialog).getByLabelText(/^Mật khẩu mới/), "mat-khau-moi");
    await user.type(within(dialog).getByLabelText(/^Nhập lại mật khẩu/), "mat-khau-moi");
    // GET /me is read twice over: by the shell's session and by the owner card.
    let meReads = 0;
    const countMe = ({ request }: { request: Request }) => {
      if (request.method === "GET" && new URL(request.url).pathname === apiUrl(PATHS.me)) meReads += 1;
    };
    server.events.on("request:start", countMe);
    try {
      await user.click(within(dialog).getByRole("button", { name: CHANGE_PASSWORD_LABEL }));
      await enterPassword(user);

      expect(await screen.findByText("Đã đổi mật khẩu. 2 thiết bị khác đã bị đăng xuất.")).toBeInTheDocument();
      expect(sent).toEqual([{ password: "mat-khau-moi" }, { password: "mat-khau-moi" }]);
      expect(reauth.calls).toHaveLength(1);
      await waitFor(() => expect(screen.queryByRole("dialog", { name: CHANGE_PASSWORD_LABEL })).not.toBeInTheDocument());
      await waitFor(() => expect(meReads).toBe(2));
    } finally {
      server.events.removeListener("request:start", countMe);
    }
  });

  it("checks the new password before sending it and shows the server's refusal on the field", async () => {
    const sent: unknown[] = [];
    server.use(
      ...accountHandlers(),
      http.post(apiUrl("/auth/password"), async ({ request }) => {
        sent.push(await request.json());
        return apiError(422, "validation_failed", { fields: { password: "too_long" } });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: CHANGE_PASSWORD_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: CHANGE_PASSWORD_LABEL });
    await user.type(within(dialog).getByLabelText(/^Mật khẩu mới/), "ngan");
    await user.type(within(dialog).getByLabelText(/^Nhập lại mật khẩu/), "khac");
    await user.click(within(dialog).getByRole("button", { name: CHANGE_PASSWORD_LABEL }));
    expect(await within(dialog).findByText("Mật khẩu cần ít nhất 8 ký tự.")).toBeInTheDocument();
    expect(within(dialog).getByText("Mật khẩu nhập lại không khớp.")).toBeInTheDocument();
    expect(sent).toEqual([]);

    await user.clear(within(dialog).getByLabelText(/^Mật khẩu mới/));
    await user.type(within(dialog).getByLabelText(/^Mật khẩu mới/), "12345678");
    await user.clear(within(dialog).getByLabelText(/^Nhập lại mật khẩu/));
    await user.type(within(dialog).getByLabelText(/^Nhập lại mật khẩu/), "12345678");
    await user.click(within(dialog).getByRole("button", { name: CHANGE_PASSWORD_LABEL }));

    expect(await within(dialog).findByText("Quá dài.")).toBeInTheDocument();
    expect(screen.getByText("Chưa ghi sổ.")).toBeInTheDocument();
    expect(sent).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: CHANGE_PASSWORD_LABEL })).toBeInTheDocument();
    expect(screen.queryByText(/^Đã đổi mật khẩu/)).not.toBeInTheDocument();
  });

  it("saves only the changed store fields under If-Match of the version read", async () => {
    const patches: { body: unknown; ifMatch: string | null }[] = [];
    const profile = testStoreProfile();
    server.use(
      ...accountHandlers({ profile }),
      http.patch(apiUrl(PATHS.tenant), async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        patches.push({ body, ifMatch: request.headers.get("If-Match") });
        return HttpResponse.json({ ...profile, ...body, version: profile.version + 1 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const name = await screen.findByRole("textbox", { name: "Tên cửa hàng" });
    await user.clear(name);
    await user.type(name, "Bánh kẹo Hòa Phát");
    await user.click(screen.getByRole("button", { name: "Lưu thông tin" }));

    expect(await screen.findByText(STORE_PROFILE_SAVED)).toBeInTheDocument();
    expect(patches).toEqual([{ body: { display_name: "Bánh kẹo Hòa Phát" }, ifMatch: `"${profile.version}"` }]);
  });

  it("sets up two-step sign-in after the password and shows the recovery codes once", async () => {
    const reauth = reauthHandler();
    const codes: unknown[] = [];
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/auth/mfa/totp"), () => HttpResponse.json({ secret: "JBSWY3DPEHPK3PXP", otpauth_uri: "otpauth://totp/Tally:an?secret=JBSWY3DPEHPK3PXP" })),
      http.post(apiUrl("/auth/mfa/totp/verify"), async ({ request }) => {
        codes.push(await request.json());
        return HttpResponse.json({ recovery_codes: ["aaaa-1111", "bbbb-2222"] });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await within(await findCard("Chủ cửa hàng")).findByText("Nguyễn Văn An");
    await unlock(user, cardOf("Chủ cửa hàng"));
    await user.click(await screen.findByRole("button", { name: MFA_SETUP_TITLE }));

    const setup = await screen.findByRole("dialog", { name: MFA_SETUP_TITLE });
    expect(within(setup).getByRole("textbox", { name: /^Khóa thiết lập/ })).toHaveValue("JBSWY3DPEHPK3PXP");
    await user.type(within(setup).getByRole("textbox", { name: /^Mã xác thực/ }), "123456");
    await user.click(within(setup).getByRole("button", { name: MFA_SETUP_TITLE }));

    expect(await screen.findByText(MFA_ENABLED_TOAST)).toBeInTheDocument();
    const list = await screen.findByRole("list", { name: "Mã khôi phục" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["aaaa-1111", "bbbb-2222"]);
    expect(reauth.calls).toEqual([{ password: "mat-khau" }]);
    expect(codes).toEqual([{ code: "123456" }]);
  });

  it("keeps the recovery codes open until the owner says they are saved, and offers copying and a file", async () => {
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.post(apiUrl("/auth/mfa/totp"), () => HttpResponse.json({ secret: "JBSWY3DPEHPK3PXP", otpauth_uri: "otpauth://totp/Tally:an?secret=JBSWY3DPEHPK3PXP" })),
      http.post(apiUrl("/auth/mfa/totp/verify"), () => HttpResponse.json({ recovery_codes: ["aaaa-1111", "bbbb-2222"] })),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await within(await findCard("Chủ cửa hàng")).findByText("Nguyễn Văn An");
    await unlock(user, cardOf("Chủ cửa hàng"));
    await user.click(await screen.findByRole("button", { name: MFA_SETUP_TITLE }));
    const setup = await screen.findByRole("dialog", { name: MFA_SETUP_TITLE });
    await user.type(within(setup).getByRole("textbox", { name: /^Mã xác thực/ }), "123456");
    await user.click(within(setup).getByRole("button", { name: MFA_SETUP_TITLE }));

    const dialog = await screen.findByRole("dialog", { name: RECOVERY_CODES_TITLE });
    const saved = within(dialog).getByRole("checkbox", { name: RECOVERY_CODES_SAVED });

    // Esc, "×", "Đóng" and the scrim are all refused while the box is unticked.
    await user.keyboard("{Escape}");
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(RECOVERY_CODES_UNSAVED);
    expect(saved).toHaveFocus();
    expect(saved).toHaveAccessibleDescription(RECOVERY_CODES_UNSAVED);
    for (const close of within(dialog).getAllByRole("button", { name: "Đóng" })) {
      await user.click(close);
      expect(screen.getByRole("dialog", { name: RECOVERY_CODES_TITLE })).toBeInTheDocument();
    }
    await user.click(document.querySelector(".ovl") as HTMLElement);
    expect(screen.getByRole("dialog", { name: RECOVERY_CODES_TITLE })).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Sao chép" }));
    expect(await screen.findByText(RECOVERY_CODES_COPIED)).toBeInTheDocument();
    expect(await navigator.clipboard.readText()).toBe("aaaa-1111\nbbbb-2222\n");
    const file = within(dialog).getByRole("link", { name: "Tải .txt" });
    expect(file).toHaveAttribute("download", RECOVERY_CODES_FILE);
    expect(file).toHaveAttribute("href", `data:text/plain;charset=utf-8,${encodeURIComponent("aaaa-1111\nbbbb-2222\n")}`);

    await user.click(saved);
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    await user.click(within(dialog).getAllByRole("button", { name: "Đóng" }).at(-1) as HTMLElement);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: RECOVERY_CODES_TITLE })).not.toBeInTheDocument());
  });

  it("asks for the lock date on the field before confirming anything", async () => {
    const posts: unknown[] = [];
    server.use(
      ...accountHandlers({ locks: testPeriodLocks({ locked_through: null, version: 0, data: [] }) }),
      http.post(apiUrl("/period-locks"), async ({ request }) => {
        posts.push(await request.json());
        return HttpResponse.json(LOCK_EVENT, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await screen.findByText("Chưa khóa kỳ nào.");
    await user.click(within(cardOf("Khóa sổ kỳ")).getByRole("button", { name: LOCK_LABEL }));

    expect(await screen.findByText("Chọn ngày khóa sổ.")).toBeInTheDocument();
    const date = screen.getByRole("textbox", { name: "Khóa sổ đến hết ngày" });
    expect(date).toHaveAttribute("aria-invalid", "true");
    expect(date).toHaveAccessibleDescription(/Chọn ngày khóa sổ\./);
    expect(screen.queryByRole("dialog", { name: LOCK_CONFIRM_TITLE })).not.toBeInTheDocument();
    expect(posts).toEqual([]);
  });

  it("holds the lock while the store is read-only and says why next to the button", async () => {
    server.use(...accountHandlers());
    server.use(withEntitlements({ can_post: false }));
    renderApp(PATH);

    await screen.findByText("Đang khóa đến hết ngày 30/09/2026.");
    const lock = within(cardOf("Khóa sổ kỳ")).getByRole("button", { name: LOCK_LABEL });
    await waitFor(() => expect(lock).toBeDisabled());
    expect(lock).toHaveAccessibleDescription(READ_ONLY_REASON);
  });

  it("locks the books through a date", async () => {
    const posts: unknown[] = [];
    server.use(
      ...accountHandlers({ locks: testPeriodLocks({ locked_through: null, version: 0, data: [] }) }),
      http.post(apiUrl("/period-locks"), async ({ request }) => {
        posts.push(await request.json());
        return HttpResponse.json({ ...LOCK_EVENT, id: "pl-2", locked_through: "2026-10-10" }, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    expect(await screen.findByText("Chưa khóa kỳ nào.")).toBeInTheDocument();
    expect(screen.getByText("Chưa khóa sổ lần nào.")).toBeInTheDocument();
    // The onboarding checklist carries the locked date too, so it is read again after the lock.
    let checklistReads = 0;
    const countChecklist = ({ request }: { request: Request }) => {
      if (request.method === "GET" && new URL(request.url).pathname === apiUrl(PATHS.onboarding)) checklistReads += 1;
    };
    server.events.on("request:start", countChecklist);
    try {
      await user.type(screen.getByRole("textbox", { name: "Khóa sổ đến hết ngày" }), "10/10/2026");
      await user.click(within(cardOf("Khóa sổ kỳ")).getByRole("button", { name: LOCK_LABEL }));

      // The lock asks first, naming the date and what changes; it takes no reason.
      const confirm = await screen.findByRole("dialog", { name: LOCK_CONFIRM_TITLE });
      expect(confirm).toHaveTextContent("Đang khóa đến");
      expect(confirm).toHaveTextContent("Chưa khóa kỳ nào");
      expect(confirm).toHaveTextContent("Khóa sổ đến");
      expect(confirm).toHaveTextContent("Hết ngày 10/10/2026");
      expect(confirm).toHaveTextContent("Cần xác nhận lại mật khẩu và nhập lý do");
      expect(within(confirm).queryByRole("textbox")).not.toBeInTheDocument();
      expect(posts).toEqual([]);
      await user.click(within(confirm).getByRole("button", { name: LOCK_LABEL }));

      expect(await screen.findByText("Đã khóa sổ đến hết ngày 10/10/2026.")).toBeInTheDocument();
      expect(posts).toEqual([{ locked_through: "2026-10-10" }]);
      await waitFor(() => expect(screen.queryByRole("dialog", { name: LOCK_CONFIRM_TITLE })).not.toBeInTheDocument());
      await waitFor(() => expect(checklistReads).toBe(1));
    } finally {
      server.events.removeListener("request:start", countChecklist);
    }
  });

  it("reopens a locked period after the password and the owner's confirmation, retrying the same intent", async () => {
    const reauth = reauthHandler();
    const reopens: Record<string, unknown>[] = [];
    let confirmation: unknown = null;
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/period-locks/reopen"), async ({ request }) => {
        reopens.push((await request.json()) as Record<string, unknown>);
        if (reopens.length === 1) return apiError(409, "exception_required", { exception_kind: "period_reopen" });
        return HttpResponse.json({ ...LOCK_EVENT, id: "pl-2", action: "reopen", locked_through: null, reason: "Sửa phiếu nhập" }, { status: 201 });
      }),
      http.post(apiUrl(PATHS.exceptionConfirmations), async ({ request }) => {
        confirmation = await request.json();
        return HttpResponse.json({ id: "exc-1" }, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await screen.findByText("Đang khóa đến hết ngày 30/09/2026.");
    await unlock(user, cardOf("Khóa sổ kỳ"));
    await user.click(within(cardOf("Khóa sổ kỳ")).getByRole("button", { name: REOPEN_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: REOPEN_LABEL });
    await user.type(within(dialog).getByRole("textbox", { name: /^Lý do/ }), "Sửa phiếu nhập");
    await user.click(within(dialog).getByRole("button", { name: REOPEN_LABEL }));

    const confirm = await screen.findByRole("dialog", { name: REOPEN_EXCEPTION_TITLE });
    await user.click(within(confirm).getByRole("checkbox", { name: REOPEN_EXCEPTION_CHECKBOX }));
    await user.click(within(confirm).getByRole("button", { name: REOPEN_LABEL }));

    expect(await screen.findByText("Đã mở lại toàn bộ kỳ đã khóa.")).toBeInTheDocument();
    expect(reopens).toEqual([
      { locked_through: null, reason: "Sửa phiếu nhập" },
      { locked_through: null, reason: "Sửa phiếu nhập", exception_confirmation_id: "exc-1" },
    ]);
    expect(confirmation).toMatchObject({ kind: "period_reopen", doc_type: "period_lock", doc_version: 1 });
    expect(reauth.calls).toHaveLength(1);
  });

  it("sends the owner transfer invitation after the password and keeps the sent notice on the card", async () => {
    const reauth = reauthHandler();
    const bodies: unknown[] = [];
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/owner-transfer"), async ({ request }) => {
        bodies.push(await request.json());
        return new HttpResponse(null, { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Chuyển quyền chủ cửa hàng");
    await unlock(user, card);
    await user.type(within(card).getByRole("textbox", { name: "Email người nhận" }), " moi@example.vn ");
    await user.type(within(card).getByRole("textbox", { name: "Họ tên người nhận" }), "Trần Thị Bình");
    await user.click(within(card).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));

    // Asks first, naming the recipient, the 72-hour window and what accepting does to this owner.
    const confirm = await screen.findByRole("dialog", { name: OWNER_TRANSFER_CONFIRM_TITLE });
    expect(confirm).toHaveAccessibleDescription("Gửi lời mời nhận quyền chủ cửa hàng tới moi@example.vn.");
    expect(confirm).toHaveTextContent("Trần Thị Bình · moi@example.vn");
    expect(confirm).toHaveTextContent("72 giờ");
    expect(confirm).toHaveTextContent("Người nhận trở thành chủ cửa hàng; tài khoản của bạn bị vô hiệu và đăng xuất trên mọi thiết bị");
    expect(bodies).toEqual([]);
    await user.click(within(confirm).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));

    expect(await within(card).findByRole("status")).toHaveTextContent(
      "Đã gửi lời mời chuyển quyền tới moi@example.vn. Người nhận cần mở liên kết trong email trong 72 giờ để chấp nhận.",
    );
    expect(bodies).toEqual([{ email: "moi@example.vn", full_name: "Trần Thị Bình" }]);
    expect(within(card).getByRole("textbox", { name: "Email người nhận" })).toHaveValue("");
  });

  it("says the invitation is being sent while it is on its way and sends it once", async () => {
    let release!: () => void;
    const answer = new Promise<void>((resolve) => {
      release = resolve;
    });
    const bodies: unknown[] = [];
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.post(apiUrl("/owner-transfer"), async ({ request }) => {
        bodies.push(await request.json());
        await answer;
        return new HttpResponse(null, { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Chuyển quyền chủ cửa hàng");
    await unlock(user, card);
    await user.type(within(card).getByRole("textbox", { name: "Email người nhận" }), "moi@example.vn");
    await user.type(within(card).getByRole("textbox", { name: "Họ tên người nhận" }), "Trần Thị Bình");
    await user.click(within(card).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));
    const confirm = await screen.findByRole("dialog", { name: OWNER_TRANSFER_CONFIRM_TITLE });
    // Let the dialog place its opening focus first, so the click below decides where focus is.
    await waitFor(() => expect(confirm).toContainElement(document.activeElement as HTMLElement));
    await user.click(within(confirm).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));

    const sending = await within(confirm).findByRole("button", { name: "Đang gửi…" });
    expect(sending).toHaveAttribute("aria-busy", "true");
    expect(sending).toHaveAttribute("aria-disabled", "true");
    expect(sending).toHaveFocus();
    await user.click(sending);
    await waitFor(() => expect(bodies).toHaveLength(1));

    release();
    expect(await within(card).findByRole("status")).toHaveTextContent("Đã gửi lời mời chuyển quyền tới moi@example.vn.");
    expect(bodies).toHaveLength(1);
  });

  it("shows no sent notice when the owner transfer invitation is refused", async () => {
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.post(apiUrl("/owner-transfer"), () => apiError(422, "email_in_use")),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Chuyển quyền chủ cửa hàng");
    await unlock(user, card);
    await user.type(within(card).getByRole("textbox", { name: "Email người nhận" }), "an@example.vn");
    await user.type(within(card).getByRole("textbox", { name: "Họ tên người nhận" }), "Nguyễn Văn An");
    await user.click(within(card).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));
    await user.click(within(await screen.findByRole("dialog", { name: OWNER_TRANSFER_CONFIRM_TITLE })).getByRole("button", { name: OWNER_TRANSFER_SUBMIT }));

    expect(await screen.findByText(/Email này đã dùng cho tài khoản khác\./)).toBeInTheDocument();
    expect(within(card).queryByRole("status")).not.toBeInTheDocument();
    expect(within(card).getByDisplayValue("an@example.vn")).toBeInTheDocument();
  });

  it("starts a full data export after the password and offers the file when it is ready", async () => {
    const reauth = reauthHandler();
    const keys: (string | null)[] = [];
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/data-exports"), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return HttpResponse.json(QUEUED_EXPORT, { status: 202 });
      }),
      http.get(apiUrl("/export-jobs/exp-1"), () =>
        HttpResponse.json(testExportJob({ status: "succeeded", finished_at: "2026-10-12T02:01:00Z", expires_at: "2026-10-19T02:01:00Z", download_path: "/api/v1/export-jobs/exp-1/download" })),
      ),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);
    await user.click(within(card).getByRole("button", { name: "Tạo bản xuất" }));

    expect(await screen.findByText(DATA_EXPORT_QUEUED)).toBeInTheDocument();
    expect(await within(card).findByRole("link", { name: "Tải về" })).toHaveAttribute("href", "/api/v1/export-jobs/exp-1/download");
    expect(keys).toHaveLength(1);
    expect(keys[0]).toMatch(/\S/);
  });

  it("announces the export being written as a status and its failure as an alert", async () => {
    let release!: () => void;
    const answer = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.post(apiUrl("/data-exports"), () => HttpResponse.json(QUEUED_EXPORT, { status: 202 })),
      http.get(apiUrl("/export-jobs/exp-1"), async () => {
        await answer;
        return HttpResponse.json(testExportJob({ status: "failed", error: "disk full" }));
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);
    await user.click(within(card).getByRole("button", { name: "Tạo bản xuất" }));

    expect(await within(card).findByRole("status")).toHaveTextContent("Đang tạo bản xuất…");
    expect(within(card).queryByRole("alert")).not.toBeInTheDocument();

    release();
    expect(await within(card).findByRole("alert")).toHaveTextContent("Không tạo được bản xuất. Bấm Tạo bản xuất để thử lại.");
    expect(within(card).getByRole("status", { name: "Bản xuất dữ liệu" })).toHaveTextContent("Lỗi");
  });

  it("finds the full data export started before a reload and offers its file", async () => {
    const created: unknown[] = [];
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.get(apiUrl("/export-jobs"), () =>
        HttpResponse.json({
          data: [
            // A report CSV still being written belongs to its report screen, not to this card.
            { ...testExportJob({ id: "exp-csv", kind: "report_csv", status: "running" }), report_key: "sales", params: {}, row_count: null, started_at: null },
            { ...testExportJob({ id: "exp-2", status: "succeeded", finished_at: "2026-10-12T02:01:00Z", expires_at: "2026-10-19T02:01:00Z", download_path: "/api/v1/export-jobs/exp-2/download" }), report_key: "", params: {}, row_count: null, started_at: null },
          ],
          next_cursor: null,
        }),
      ),
      http.get(apiUrl("/export-jobs/exp-2"), () =>
        HttpResponse.json(testExportJob({ id: "exp-2", status: "succeeded", finished_at: "2026-10-12T02:01:00Z", expires_at: "2026-10-19T02:01:00Z", download_path: "/api/v1/export-jobs/exp-2/download" })),
      ),
      http.post(apiUrl("/data-exports"), () => {
        created.push(true);
        return HttpResponse.json(QUEUED_EXPORT, { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);

    expect(await within(card).findByRole("link", { name: "Tải về" })).toHaveAttribute("href", "/api/v1/export-jobs/exp-2/download");
    expect(created).toEqual([]);
  });

  it("does not bring back a full data export that can no longer be downloaded", async () => {
    let jobReads = 0;
    let listReads = 0;
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.get(apiUrl("/export-jobs"), () => {
        listReads += 1;
        return HttpResponse.json({
          data: [
            { ...testExportJob({ id: "exp-3", status: "expired", finished_at: "2026-10-01T02:01:00Z", expires_at: "2026-10-08T02:01:00Z" }), report_key: "", params: {}, row_count: null, started_at: null },
            { ...testExportJob({ id: "exp-4", status: "failed", error: "disk full" }), report_key: "", params: {}, row_count: null, started_at: null },
          ],
          next_cursor: null,
        });
      }),
      http.get(apiUrl("/export-jobs/:id"), () => {
        jobReads += 1;
        return HttpResponse.json(testExportJob());
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);

    await waitFor(() => expect(listReads).toBe(1));
    expect(await within(card).findByRole("button", { name: "Tạo bản xuất" })).toBeEnabled();
    expect(within(card).queryByText("Đang tạo bản xuất…")).not.toBeInTheDocument();
    expect(within(card).queryByText("Hết hạn tải")).not.toBeInTheDocument();
    expect(within(card).queryByText("Lỗi")).not.toBeInTheDocument();
    expect(jobReads).toBe(0);
  });

  it("sends the data export again under the same key after the password the API asks for", async () => {
    const reauth = reauthHandler();
    const sent: { key: string | null; body: string }[] = [];
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/data-exports"), async ({ request }) => {
        sent.push({ key: request.headers.get("Idempotency-Key"), body: await request.text() });
        return sent.length === 1 ? apiError(403, "reauth_required") : HttpResponse.json(QUEUED_EXPORT, { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);
    await user.click(within(card).getByRole("button", { name: "Tạo bản xuất" }));
    await enterPassword(user);

    expect(await screen.findByText(DATA_EXPORT_QUEUED)).toBeInTheDocument();
    expect(sent).toHaveLength(2);
    expect(sent[0]?.key).toMatch(/\S/);
    expect(sent[1]).toEqual(sent[0]);
  });

  it("presses the data export again after a lost answer under the same key, and a new export gets a new one", async () => {
    const keys: (string | null)[] = [];
    server.use(
      ...accountHandlers(),
      reauthHandler().handler,
      http.post(apiUrl("/data-exports"), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json(QUEUED_EXPORT, { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Xuất toàn bộ dữ liệu");
    await unlock(user, card);
    const press = async (sends: number) => {
      await user.click(within(card).getByRole("button", { name: "Tạo bản xuất" }));
      await waitFor(() => expect(keys).toHaveLength(sends));
      await waitFor(() => expect(within(card).getByRole("button", { name: "Tạo bản xuất" })).toBeEnabled());
    };
    await press(1);
    await press(2);
    expect(await screen.findByText(DATA_EXPORT_QUEUED)).toBeInTheDocument();
    await press(3);

    expect(keys[0]).toMatch(/\S/);
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[0]);
  });

  it("asks to close the account after the password and the confirmation", async () => {
    const reauth = reauthHandler();
    let asked = 0;
    server.use(
      ...accountHandlers(),
      reauth.handler,
      http.post(apiUrl("/account-closure"), () => {
        asked += 1;
        return HttpResponse.json(testClosureRequest(), { status: 202 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const card = await findCard("Đóng tài khoản");
    await within(card).findByRole("button", { name: CONFIRM_PASSWORD });
    await unlock(user, card);
    await user.click(within(card).getByRole("button", { name: CLOSURE_REQUEST_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: CLOSURE_REQUEST_LABEL });
    await user.click(within(dialog).getByRole("button", { name: CLOSURE_REQUEST_LABEL }));

    expect(await screen.findByText("Đã gửi liên kết xác nhận đóng tài khoản tới email chủ cửa hàng.")).toBeInTheDocument();
    expect(asked).toBe(1);
  });

  const cancellable: ReadonlySet<ClosureStatus> = new Set(["requested", "verified", "waiting"]);
  it.each(CLOSURE_STATUSES.map((s) => [s, cancellable.has(s)] as const))("offers cancelling a closure in status %s: %s", async (status, offered) => {
    server.use(...accountHandlers({ closure: { request: testClosureRequest({ status }), export: null } }));
    renderApp(PATH);

    const card = await findCard("Đóng tài khoản");
    await within(card).findByText("Trạng thái");
    expect(within(card).queryByRole("button", { name: CLOSURE_CANCEL_LABEL }) !== null).toBe(offered);
  });

  it("cancels a waiting closure without asking for the password", async () => {
    let cancelled = 0;
    server.use(
      ...accountHandlers({ closure: { request: testClosureRequest({ status: "waiting", verified_at: "2026-10-12T03:00:00Z" }), export: null } }),
      http.post(apiUrl("/account-closure:cancel"), () => {
        cancelled += 1;
        return HttpResponse.json(testClosureRequest({ status: "cancelled" }));
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: CLOSURE_CANCEL_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: CLOSURE_CANCEL_LABEL });
    await user.click(within(dialog).getByRole("button", { name: CLOSURE_CANCEL_LABEL }));

    expect(await screen.findByText(CLOSURE_CANCELLED)).toBeInTheDocument();
    expect(cancelled).toBe(1);
    expect(screen.queryByRole("dialog", { name: REAUTH_TITLE })).not.toBeInTheDocument();
  });

  it("offers resending the link while the closure waits for the e-mail", async () => {
    server.use(...accountHandlers({ closure: { request: testClosureRequest({ status: "requested" }), export: null } }));
    renderApp(PATH);

    const card = await findCard("Đóng tài khoản");
    expect(await within(card).findByText("Gửi lại liên kết xác nhận cần xác nhận lại mật khẩu.")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: CLOSURE_RESEND_LABEL })).not.toBeInTheDocument();
  });

  it("shows the failed attempts of the final export", async () => {
    server.use(
      ...accountHandlers({
        closure: { request: testClosureRequest({ status: "export_failed", export_job_id: "exp-9" }), export: { id: "exp-9", status: "failed", expires_at: null, final: true, failed_attempts: 3 } },
      }),
    );
    renderApp(PATH);

    const card = await findCard("Đóng tài khoản");
    expect(await within(card).findByText("Bản xuất cuối bị lỗi, chỉ xem")).toBeInTheDocument();
    expect(card).toHaveTextContent("Bản xuất cuối");
    expect(card).toHaveTextContent("Lần lỗi 3/3");
    expect(within(card).queryByRole("button", { name: CONFIRM_PASSWORD })).not.toBeInTheDocument();
  });

  it("shows two-step sign-in as on for an owner who set it up", async () => {
    server.use(...accountHandlers({ owner: testOwner({ mfa_enabled: true }), closure: NO_CLOSURE }));
    renderApp(PATH);

    expect(await within(await findCard("Chủ cửa hàng")).findByText("Đã bật")).toBeInTheDocument();
  });
});
