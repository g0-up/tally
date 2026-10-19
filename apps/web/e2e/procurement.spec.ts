import { expect, test, type Locator, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// The purchasing journey on the seeded store's real session: an order that creates its product
// and supplier on the fly, received in part with lot, expiry and color, a receipt over the 5%
// tolerance refused, a lot returned to the supplier, the rest closed short. Stock and the
// supplier payable are read back from the API. Every run uses fresh names, so runs never collide.

interface Page_<T> {
  data: T[];
}
interface SkuRow {
  id: string;
  display_name: string;
  on_hand: string;
}
interface DocRef {
  id: string;
  doc_no: string;
}
interface ReceiptDetail {
  id: string;
  payable?: { amount: string; open_amount: string } | null;
}
interface Reason {
  id: string;
  name: string;
  active: boolean;
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const labelled = (label: string, name: string) => new RegExp(`^${escapeRe(label)} – ${escapeRe(name)}`);

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/** Text of the bold document number in the toast that matches `text`. */
async function toastDoc(page: Page, text: RegExp): Promise<string> {
  const toast = page.getByText(text).first();
  await expect(toast).toBeVisible();
  return ((await toast.locator("b").first().textContent()) ?? "").trim();
}

/** Records a step the seeded store cannot run, so the report shows what was not covered. */
function skippedStep(description: string): void {
  test.info().annotations.push({ type: "skipped step", description });
}

async function activeReasons(page: Page, kind: string): Promise<Reason[]> {
  const r = await apiGet<Page_<Reason>>(page, `/reasons?kind=${kind}&active=true`);
  return r.data.filter((x) => x.active);
}

async function onHandOf(page: Page, name: string): Promise<number> {
  const r = await apiGet<Page_<SkuRow>>(page, `/skus?q=${encodeURIComponent(name)}&limit=10`);
  const sku = r.data.find((s) => s.display_name === name);
  expect(sku, `SKU ${name} listed`).toBeDefined();
  return Number(sku?.on_hand ?? Number.NaN);
}

async function receiptOf(page: Page, docNo: string): Promise<ReceiptDetail> {
  const list = await apiGet<Page_<DocRef>>(page, `/receipts?q=${encodeURIComponent(docNo)}&limit=50`);
  const ref = list.data.find((d) => d.doc_no === docNo);
  expect(ref, `receipt ${docNo} listed`).toBeDefined();
  return apiGet<ReceiptDetail>(page, `/receipts/${ref?.id ?? ""}`);
}

async function supplierRow(page: Page, name: string): Promise<Locator> {
  await openScreen(page, "/catalog/suppliers");
  await page.getByPlaceholder("Tìm tên, mã, số điện thoại, người liên hệ, mặt hàng…").fill(name);
  const row = page.getByRole("row").filter({ hasText: name });
  await expect(row).toHaveCount(1);
  return row;
}

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("orders a new product, receives part of it, returns a lot and closes the rest short", async ({ page }) => {
    test.slow();
    const stamp = Date.now().toString(36);
    const supplierName = `NCC e2e ${stamp}`;
    const productName = `Bánh e2e ${stamp}`;
    const lotNo = `L-${stamp}`;

    // A new order with a supplier and a product that do not exist yet: 2 thùng of 12 hộp at 120.000 ₫.
    await openScreen(page, "/purchasing/orders");
    await page.getByRole("button", { name: "Tạo đơn nhập" }).click();
    const newPo = page.getByRole("dialog", { name: "Tạo đơn nhập" });
    await newPo.getByRole("button", { name: "+ NCC mới" }).click();
    const quick = page.getByRole("dialog", { name: "Thêm nhà cung cấp mới" });
    await quick.getByLabel(/^Tên nhà cung cấp/).fill(supplierName);
    await quick.getByRole("button", { name: "Thêm & chọn" }).click();
    await expect(quick).toBeHidden();
    await expect(newPo.getByRole("combobox", { name: "Nhà cung cấp" })).toHaveValue(supplierName);

    await newPo.getByRole("button", { name: "+ Hàng hóa mới" }).click();
    await newPo.getByRole("textbox", { name: "Tên hàng mới" }).last().fill(productName);
    await expect(newPo.getByRole("textbox", { name: labelled("Hệ số quy đổi", productName) })).toHaveValue("12");
    await expect(newPo.getByRole("checkbox", { name: labelled("Quản lý lô/hạn", productName) })).toBeChecked();
    await newPo.getByRole("textbox", { name: labelled("Số lượng", productName) }).fill("2");
    await newPo.getByRole("textbox", { name: labelled("Đơn giá", productName) }).fill("120000");
    await newPo.getByRole("textbox", { name: labelled("Giá bán lẻ", productName) }).fill("15000");
    await newPo.getByRole("radiogroup", { name: labelled("Màu sắc", productName) }).getByRole("radio", { name: "Đỏ" }).click();
    await newPo.getByRole("button", { name: "Lưu & chốt đơn" }).click();
    const poDoc = await toastDoc(page, /: Đã chốt\./);
    expect(poDoc).toMatch(/^PO-/);
    await expect(newPo).toBeHidden();

    // The open order keeps the supplier trading: "Ngừng" stays reachable, says why and stops nothing.
    const before = await supplierRow(page, supplierName);
    const stopBefore = before.getByRole("button", { name: `Ngừng ${supplierName}`, exact: true });
    await expect(stopBefore).toBeEnabled();
    await expect(stopBefore).toHaveAccessibleDescription(/Còn đơn nhập đang mở/);
    await stopBefore.click();
    const notYet = page.getByRole("dialog", { name: `Chưa ngừng được giao dịch với ${supplierName}` });
    await expect(notYet.getByRole("alert")).toContainText("Còn đơn nhập đang mở");
    await expect(notYet.getByRole("button", { name: "Ngừng giao dịch" })).toHaveCount(0);
    await notYet.getByRole("button", { name: "Đóng", exact: true }).last().click();
    await expect(notYet).toBeHidden();

    // Receiving counts base units: 26 hộp is over 24 + 5% and is refused; 12 hộp of a lot posts.
    await openScreen(page, "/purchasing/orders");
    await page.getByPlaceholder("Tìm số đơn, nhà cung cấp…").fill(poDoc);
    const poRow = page.getByRole("row").filter({ hasText: poDoc });
    await expect(poRow).toHaveCount(1);
    await expect(poRow).toContainText("Đã chốt");
    await poRow.getByRole("button", { name: "Nhận hàng" }).click();
    const receive = page.getByRole("dialog", { name: `Nhận hàng theo ${poDoc}` });
    const qty = receive.getByRole("textbox", { name: labelled("Số lượng", productName) });
    await expect(qty).toHaveValue("24");
    await qty.fill("26");
    await receive.getByRole("textbox", { name: labelled("Số lô", productName) }).fill(lotNo);
    const expiry = receive.getByRole("textbox", { name: labelled("Hạn dùng", productName) });
    if ((await expiry.count()) > 0) await expiry.fill("31/12/2027");
    await receive.getByRole("button", { name: "Ghi sổ nhập kho" }).click();
    await expect(page.getByText(/vượt dung sai 5% so với còn chờ 24/).first()).toBeVisible();
    await expect(receive).toBeVisible();

    await qty.fill("12");
    await receive.getByRole("button", { name: "Ghi sổ nhập kho" }).click();
    const pnDoc = await toastDoc(page, /Tồn, nợ NCC và giá bán đã cập nhật\./);
    expect(pnDoc).toMatch(/^PN-/);
    await expect(receive).toBeHidden();
    await expect(poRow).toContainText("Nhận một phần");

    expect(await onHandOf(page, productName)).toBe(12);
    const received = await receiptOf(page, pnDoc);
    expect(Number(received.payable?.amount)).toBe(120000);
    expect(Number(received.payable?.open_amount)).toBe(120000);

    // Two hộp of the lot go back to the supplier and come off what the store owes.
    let expectedStock = 12;
    let expectedOpen = 120000;
    if ((await activeReasons(page, "purchase_return")).length === 0) {
      skippedStep("Trả NCC: the store has no purchase_return reason");
    } else {
      await openScreen(page, `/purchasing/receipts?doc=${pnDoc}`);
      const drawer = page.getByRole("dialog", { name: `Phiếu nhận ${pnDoc}` });
      await expect(drawer).toContainText(lotNo);
      await drawer.getByRole("button", { name: "Trả NCC từ phiếu này" }).click();
      const ret = page.getByRole("dialog", { name: "Trả hàng nhà cung cấp" });
      await ret.getByRole("textbox", { name: `SL trả – ${productName}` }).fill("2");
      await ret.getByRole("button", { name: "Ghi sổ xuất trả" }).click();
      await expect(page.getByText("Đã ghi phiếu trả NCC.").first()).toBeVisible();
      await expect(ret).toBeHidden();
      expectedStock = 10;
      expectedOpen = 100000;
    }
    await expect.poll(() => onHandOf(page, productName)).toBe(expectedStock);
    await expect.poll(async () => Number((await receiptOf(page, pnDoc)).payable?.open_amount)).toBe(expectedOpen);

    // The 12 hộp still pending are closed short; with nothing open the supplier can stop trading.
    if ((await activeReasons(page, "close_short")).length === 0) {
      skippedStep("Đóng phần còn thiếu: the store has no close_short reason");
      return;
    }
    await openScreen(page, `/purchasing/orders?doc=${poDoc}`);
    const poDrawer = page.getByRole("dialog", { name: `Đơn nhập ${poDoc}` });
    await expect(poDrawer).toContainText(pnDoc);
    await poDrawer.getByRole("button", { name: "Đóng phần còn thiếu" }).click();
    const close = page.getByRole("dialog", { name: `Đóng phần còn thiếu của ${poDoc}` });
    const reason = close.getByRole("combobox", { name: /^Lý do/ });
    await expect(reason.locator("option").nth(1)).toBeAttached();
    await reason.selectOption({ index: 1 });
    await close.getByRole("button", { name: "Đóng đơn" }).click();
    await expect(page.getByText("Đã đóng phần còn thiếu.").first()).toBeVisible();
    await expect(poDrawer).toContainText("Đóng – thiếu");
    expect(await onHandOf(page, productName)).toBe(expectedStock);

    const after = await supplierRow(page, supplierName);
    const stopAfter = after.getByRole("button", { name: `Ngừng ${supplierName}`, exact: true });
    await expect(stopAfter).toBeEnabled();
    await expect(stopAfter).not.toHaveAttribute("aria-describedby");
    await stopAfter.click();
    const confirmStop = page.getByRole("dialog", { name: `Ngừng giao dịch với ${supplierName}?` });
    await confirmStop.getByRole("button", { name: "Ngừng giao dịch", exact: true }).click();
    await expect(page.getByText("Đã ngừng giao dịch. NCC không còn hiện khi tạo đơn nhập.").first()).toBeVisible();
    await expect(confirmStop).toBeHidden();
  });
});
