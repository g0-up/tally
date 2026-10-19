# Support checklist

What a support agent does when a store owner asks for help: triage, reading the store's data, ledger
drift, incidents, billing problems, account closure and restore. Owners' own instructions are in the
[user guide](../user-guide/README.md); operator commands are in the [runbook](../runbook.md) and
[tenant restore guide](../operations/tenant-restore.md).

Production commands below run inside the API container of the production project:

```sh
docker compose -p tally-prod exec api /tally <command>
```

`/tally <command> --help` prints the flags of each command.

## Principles

- **Never change a balance in the database.** Stock, debts and funds change only through documents
  the owner posts (receipts, payments, adjustments with a reason, reversals) or through the restore
  procedure. Support sessions are read-only by construction.
- **Never ask for a password, a one-time code or a recovery code**, and do not paste one into a
  ticket. The owner's ticket form already warns when the text looks like a password.
- **Data the store entered is the store's.** Explain what the ledger shows and how to correct it with
  a document; the owner decides and posts.
- **Support hours are not published yet.** They must be published before selling (PRD §14.3); there
  is no 24/7 commitment. The owner fills them in here once decided.

## 1. Triage

Every ticket arrives in the admin console under **Hỗ trợ** (`/admin/support`). Open it, take it with
**Giao cho tôi** (or **Phân công** to someone else), set **Trạng thái** and **Ưu tiên**, and reply
with **Gửi trả lời**.

Classify by the PRD §14.3 categories and start with the first action of the category:

| Category | Typical report | First action |
|---|---|---|
| Data loss or leak (mất/rò dữ liệu) | "Chứng từ biến mất", data of another store visible | Highest priority. Get the document number and time. A suspected cross-store leak is a security incident: publish an incident (§4), sign the store out if an account may be compromised (§3), and escalate to the operator. |
| Wrong books (sai sổ) | Stock, debt or fund total does not match the documents | Run `ledger verify` for the store (§3). |
| Cannot transact (không giao dịch được) | POS or posting fails for everyone | Check whether the store is read-only (subscription status on the admin tenant page), held in maintenance, or affected by an open incident. Check API logs with `make prod-logs SERVICE=api`. |
| Functional bug (lỗi chức năng) | A screen misbehaves | Reproduce in a sandbox store, never in the customer's store. Record steps, browser and device. |
| Advice (yêu cầu tư vấn) | "Làm sao để…" | Answer from the [user guide](../user-guide/README.md). |

Look up the store first on **Cửa hàng** (`/admin/tenants`): the tenant page shows its subscription,
closure and legal-hold state. Its tenant id is the last part of the page address
(`/admin/tenants/<tenant id>`); the CLI commands below need it.

## 2. Support sessions (reading a store's data)

Support can read a store only when the owner grants it, for one ticket, for a chosen set of modules
and a limited time.

1. The ticket must be open and assigned to you. A store cannot grant access on a ticket without an
   assigned agent.
2. Ask the owner to open **Thiết lập › Thuê bao › Truy cập hỗ trợ**, choose the ticket and the
   modules, and press **Cấp quyền** (owner steps:
   [Truy cập hỗ trợ](../user-guide/cai-dat-goi-bao-mat.md#truy-cập-hỗ-trợ)). The owner screen grants
   two hours; the API caps a session at eight.
3. Open the session from **Phiên hỗ trợ của tôi** on `/admin/support`. Each view opens at
   `/admin/support/tenants/<tenant id>/<module>` and only accepts the routes of the granted modules.
4. The session ends at its expiry or when the owner presses **Thu hồi**.

What protects the store:

- every read runs in a read-only transaction as the `tally_readonly` database role (see the runbook's
  "2. Database roles"), so a support session cannot post or edit anything;
- the store's **Nhật ký** records `support_session.grant`, every `support_session.access` and
  `support_session.revoke`, so the owner sees who read what and when;
- outside an active session, the admin console shows the store's subscription, usage and tickets, not
  its business data.

Do not take screenshots of customer data into the ticket. Quote document numbers instead.

## 3. Ledger drift

A projection (stock on hand, debt balance, fund balance, document totals) must always equal the sum
of its append-only ledger. When an owner reports wrong totals:

1. Verify the store's ledgers (read-only, safe on production):

   ```sh
   docker compose -p tally-prod exec api /tally ledger verify --tenant <tenant id>
   ```

   Add `--sku <sku id>` to check only the stock ledger of one SKU. Without `--tenant` it checks every
   store.

2. Read the output:
   - `verified 1 tenants: 0 mismatches` and exit status 0: the books are consistent. The problem is in
     the documents the owner posted (a wrong quantity, a payment allocated to the wrong document).
     Find the document with the owner in a support session and explain the correcting document
     (return, adjustment with a reason, reversal, new payment allocation).
   - one line per mismatch, `tenant <id> <domain>: <detail>`, where the domain is `debt`, `cash`,
     `inventory`, `procurement`, `sales` or `kits`, then a non-zero exit with
     `N projections disagree with their ledgers`: this is a software defect. Continue with step 3.

3. On a real mismatch:
   - Stop new postings from making it worse while the cause is found:

     ```sh
     docker compose -p tally-prod exec api /tally tenant maintenance on --tenant <tenant id>
     ```

     Every change to that store is refused (`423 tenant_maintenance`) and its background jobs,
     exports included, are skipped until `tenant maintenance off --tenant <tenant id>`; reads keep
     working. Tell the owner before and after (template in §4).
   - Escalate to the engineer on call with the full `ledger verify` output and the ticket number.
   - Dry-run the rebuild. Without `--confirm` it writes nothing (`--dry-run` is the default):

     ```sh
     docker compose -p tally-prod exec api /tally ledger rebuild --tenant <tenant id>
     ```

     It holds every posting lock of the store while it runs, so the store's postings wait. It prints
     each difference marked `rebuild` (the rebuild recomputes it) or `manual` (it stays), the rows it
     would rewrite per check, and ends with `dry run of tenant <id>: N differences, M to rebuild, K
     to fix by hand; nothing written`.
   - When K is not 0, the rebuild cannot fix the store: a ledger gives no valid value, or a document
     disagrees with its own lines. `--confirm` would roll back. Repair from a backup with the
     [tenant restore guide](#7-tenant-restore), or with a reviewed migration.
   - When K is 0, take a backup first, because only restoring it undoes a rebuild. Then write:

     ```sh
     make db-backup STACK=prod
     docker compose -p tally-prod exec api /tally ledger rebuild --tenant <tenant id> --confirm
     ```

     The rebuild verifies the store again in the same transaction. When it is clean, it commits,
     records `ledger.rebuild` in the store's audit log and prints `rebuilt tenant <id>: N rows;
     verified: 0 differences`. When differences remain, everything is rolled back and the command
     fails with `rolled back, nothing written`.
   - Verify again with `ledger verify --tenant <tenant id>` (step 1). Release maintenance only after
     it reports 0 mismatches.

4. Record the verify output, the cause and the repair on the ticket.

### Signing a store out

When an account may be compromised, sign out every session of the store; the reason is recorded on
the sessions and in the store's audit log:

```sh
docker compose -p tally-prod exec api /tally maintenance revoke-sessions --tenant <tenant id or slug> --reason <short reason>
```

It prints `revoked N sessions of tenant <tenant>`. Ask the owner to change their password after
signing in again.

## 4. Incident communication

Publish incidents in the admin console under **Sự cố** (`/admin/incidents`):

- **Công bố sự cố** with a title, impact and a first message as soon as more than one store is
  affected, or one store cannot transact.
- **Cập nhật sự cố** on each change. Each update adds a line to the history. The statuses are **Đang
  điều tra**, **Đã xác định nguyên nhân**, **Đang theo dõi** and **Đã khắc phục**.

The API serves unresolved incidents and those resolved in the last seven days at `GET /status`, but
**no screen of the store app shows them yet**. Until it does, also reply on the affected tickets.

Message templates (Vietnamese, for owners). Never include internal hostnames, logs or other stores'
names.

- Đang điều tra: "Tally đang gặp sự cố: <ảnh hưởng, ví dụ không ghi sổ được>. Chúng tôi đang điều tra.
  Dữ liệu đã ghi sổ không bị mất. Cập nhật tiếp theo lúc <giờ>."
- Tạm dừng ghi sổ (maintenance): "Để kiểm tra số liệu, chúng tôi tạm dừng ghi sổ của cửa hàng từ <giờ>.
  Bạn vẫn xem được dữ liệu. Chúng tôi sẽ báo khi mở lại."
- Đã khắc phục: "Sự cố đã được khắc phục lúc <giờ>. Nguyên nhân: <tóm tắt>. Nếu thấy số liệu khác
  thường, hãy trả lời yêu cầu hỗ trợ này kèm số chứng từ."

## 5. Payments and refunds

Service invoices (the store's subscription bills) are listed under **Hóa đơn thuê bao**
(`/admin/invoices`).

- **Bank transfer received:** press **Đã thanh toán** on the invoice, enter **Mã tham chiếu chuyển
  khoản** and press **Ghi nhận đã thanh toán**. The plan change or add-on takes effect when the
  invoice is paid.
- **Wrong invoice:** **Hủy** → **Hủy hóa đơn**.
- **Refused payment, refund by hand:** a payment confirmed for an invoice that is no longer open
  (for example voided when the store closed) is refused and changes nothing in the store, but the
  money may already have been taken. The API logs
  `payment refused: its invoice is not open, refund it by hand` with the tenant, invoice, provider,
  event id, amount and reference, and the store's audit log records `saas_invoice.payment_refused`
  once per provider event. Refund the amount through the provider and note the refund on the ticket.
  Details: [tenant restore guide, §4](../operations/tenant-restore.md#4-purge-a-closed-store).

An unpaid subscription past its grace period makes the store read-only: the owner still reads and
exports everything, nothing is deleted, and debts stay visible. On the admin tenant page **Gia hạn**
extends the subscription and **Kích hoạt lại** ends a suspension made with **Tạm ngưng**. Plan prices
are not published yet ("Chưa chốt").

## 6. Account closure and data export

The owner closes their own account and exports their own data; the steps are in the user guide
([Đóng tài khoản](../user-guide/cai-dat-goi-bao-mat.md#đóng-tài-khoản),
[Xuất toàn bộ dữ liệu](../user-guide/cai-dat-goi-bao-mat.md#xuất-toàn-bộ-dữ-liệu)). In short: the
closure is confirmed from an e-mail link, the store keeps working for 30 days and can cancel, then it
turns read-only, a final full export is made and mailed, and the store closes. A full export zip can
be downloaded for 7 days.

Support's part:

- **Final export failed:** the tenant page's **Đóng tài khoản** card says the final export failed and
  the store has not closed. Fix the cause, then **Xuất lại dữ liệu** → **Xuất lại** starts a new round.
  The console has no button to re-send the download link of a completed closure; the operator does it
  with the API call in [tenant restore guide, §4](../operations/tenant-restore.md#4-purge-a-closed-store).
- **Legal hold:** **Bật giữ pháp lý** / **Gỡ giữ pháp lý** on the tenant page. A store under legal
  hold does not close at the end of the wait. Clear a hold only when it has really ended.
- **Purging a closed store** deletes its rows and cannot be undone except from a backup. Follow
  [tenant restore guide, §4](../operations/tenant-restore.md#4-purge-a-closed-store) exactly. Files in
  the object store are not deleted by the purge yet (open retention decision); record the printed
  prefixes on the ticket.

## 7. Tenant restore

When a store lost data (an owner deleted the wrong thing, or a defect damaged rows) and the cause is
understood:

1. Restore the backup into a scratch database: the runbook's "3. Backup and restore" and
   [tenant restore guide, §1](../operations/tenant-restore.md#1-restore-the-backup-into-a-scratch-database).
2. Check the store in the backup with `tally tenant restore-check --tenant <tenant id> --scratch-url
   <scratch url>`, which counts its rows there and verifies its ledgers
   ([§2](../operations/tenant-restore.md#2-check-the-tenant-in-the-backup)).
3. Hold the store with `tally tenant maintenance on --tenant <tenant id>` and copy the missing rows
   with `tally tenant copy --tenant <tenant id> --from <scratch url>`
   ([§3](../operations/tenant-restore.md#3-copy-the-missing-rows-into-the-live-database)).
4. Run `tally ledger verify --tenant <tenant id>`, release the store with
   `tally tenant maintenance off --tenant <tenant id>`, and tell the owner what was restored.

Never type a connection string with its password into the ticket.
