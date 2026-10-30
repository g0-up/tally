# Browser, device and peripheral matrix

The release gate for NFR-10 (desktop and tablet only, last two major versions of each browser, real
tablets, certified peripherals) and NFR-11 (keyboard access, visible focus, WCAG 2.2 AA). The automated part runs in
Playwright; the rest is a manual pass recorded per release. The UI gate that these checks belong to is
described in the [phase 20 plan](../../plans/261016-0854-tally-full-product-implementation/phase-20-ui-ux-review-accessibility.md).

## 1. Matrix

Tally supports desktop browsers and tablets, landscape and portrait (768 px and wider). Phone screens are
not a supported target: a phone takes part only as the paired camera scanner, whose scan page is the one
screen built for it.

| Row | Target | Automated | Manual pass |
|---|---|---|---|
| Chrome, desktop | last two major versions | `chromium` project: functional, parity, axe, keyboard, focus, targets | none beyond the automated pass |
| Edge, desktop | last two major versions | `a11y-msedge` project (CI only, `msedge` channel) | none |
| Firefox, desktop | last two major versions | `firefox` project: functional journeys | NVDA on Windows with Firefox (§4) |
| Safari, macOS | last two major versions | `webkit` project: functional journeys | optional VoiceOver on macOS |
| Android, real tablet | Chrome, current and previous major | none (no device in CI) | POS sale, stock count scan, offline banner (§3) |
| iPadOS, real tablet | Safari, current and previous major | none | VoiceOver pass on POS, invoice, exception, debt payment, offline banner (§4) |
| Barcode scanner | USB or Bluetooth HID keyboard wedge | none | scan into the POS input and the stock count (§3) |
| Paired phone as scanner | Android Chrome and iOS Safari, current and previous major | none (unit tests fake the camera and reader) | pairing, scans into POS, stock count and purchasing (§3) |
| Receipt printer | 80 mm thermal, certified driver | none | print `/print/sales-documents/:id` (§3) |
| A4/A5 printer | laser, system dialog | none | print a sales document (A5) and `/print/statements/:id` (A4) (§3) |

Parity against the prototype is pixel-compared on Linux only; other browsers run the functional and
accessibility specs, because font rendering differs across operating systems.

## 2. Automated runs

Start the stack first (`make dev`). The specs need the seeded store's owner, so set
`E2E_OWNER_PASSWORD` in the shell; never write it into a file. Run from `apps/web`:

```sh
corepack pnpm e2e:visual                              # prototype parity at 1440 and 390 px
corepack pnpm e2e:a11y                                # axe, keyboard, focus, touch targets
corepack pnpm e2e -- --project=firefox --project=webkit   # functional journeys on other engines
```

Two states need an id of a seeded document, and they are skipped with a reason when it is not set:
`E2E_SALES_DOC_ID` for `/print/sales-documents/:id` and `E2E_STATEMENT_ID` for `/print/statements/:id`.
The "Đặt lại mẫu" dialog exists only on a demo store, so its state runs only with `E2E_SANDBOX_STORE`
set and `E2E_TENANT_URL` pointing at a demo store; the store of `make seed` is a real one.

Parity additions (screens without a prototype) have committed baselines under `e2e/visual/`. A new
addition or a visual change is reviewed by a person before `--update-snapshots` is run for it.

## 3. Manual checks on devices and peripherals

Run each row on the device, record the outcome, and note the device model, OS and browser version.

- **Android Chrome (tablet):** sign in, open the POS, pick a colour, take cash and check the change, hold a
  cart, reopen it. Repeat a stock count with a paired phone in the scanner's place. Turn on airplane mode, check the
  offline banner appears and that a sale queued offline syncs when the network returns.
- **iPadOS Safari (tablet):** the same POS journey, in landscape and portrait, plus the invoice confirm with exception and a debt payment. Check
  that the menu and each dialog close with their on-screen close control.
- **HID barcode scanner:** scan an item into the POS input and into the stock count. A scan must add one
  line per code without a click, and must not submit a half-typed code.
- **Paired phone as scanner (Android Chrome and iOS Safari, each):**
  - Pair from the POS card "Quét bằng điện thoại": the verify code is the same on both screens, the
    phone cannot start its camera before "Cho phép", and "Từ chối" sends the phone to its refused
    message while the desktop shows a new QR code.
  - Scan an EAN-13, a UPC-A, a carton code (POS adds a carton line; the stock count adds the carton's
    factor) and a code the catalog does not have (POS and the count refuse it; a purchase order opens a
    new-product row carrying it). Each scan shows its line on the phone within about a second, with
    vibration on Android and a beep plus screen flash on iOS after the "Bật camera" tap.
  - Disconnect from each side ("Ngắt" on the desktop, "Ngắt kết nối" on the phone).
  - Lock the phone screen and unlock it: the camera is off, "Bật camera" resumes without pairing again.
  - Move from the POS to a stock count in the same tab: scans follow without pairing again.
  - Restart `api` while paired: both sides show "Đang kết nối lại…" and scanning resumes without a new
    QR scan; note how long it was interrupted.
  - On a phone that still has the PWA of the previous release, open the QR code the first time and record
    what appears; the user guide's reload steps must lead to a working scan page.
  - Sign out on the desktop while paired: the phone reports the session ended within about 15 seconds.
- **Receipt printer (80 mm):** print a sales document from `/print/sales-documents/:id` with "Giấy cuộn
  80mm". The text must not wrap or clip at the paper width.
- **A4/A5 printer:** print a sales document on A5 ("Khổ A5") and a debt statement from
  `/print/statements/:id` on A4; the page breaks must not split a line of a document.

Use a sandbox store for every manual run. Do not photograph customer data for the record, and never
record a pairing QR code, a verify code or a scanner cookie.

## 4. Screen-reader passes

NVDA with Firefox (Windows) and VoiceOver with Safari (iPadOS) read these screens: POS, the invoice dialog,
the exception dialog, the debt payment dialog and the offline banner. Check that:

- the offline banner is announced as a status (`role="status"`), sync results are announced politely
  (`aria-live="polite"`), and errors are announced as alerts (`role="alert"`);
- every dialog announces its title when it opens and returns focus to the control that opened it;
- every table field reads with its row context (for example "SL – {tên hàng}").

Record what each screen reader announced, not only pass or fail.

## 5. Recording results

Write one report per release under `plans/reports/`, named `tester-<YYMMDD>-<HHMM>-device-matrix.md`.
It lists, per row of §1: the browser or device and its version, the date, the outcome, and any defect
with a link to its issue. Keep the report free of credentials, tokens and personal data; screenshots
must show the sandbox store only.

## 6. Certified peripherals

The peripherals Tally supports for release. A model is listed only after it passed its §3 check on a
release build; the report of §5 that passed it is its evidence. Until a run, nothing is certified and
owners are told to test their own device in a sandbox store first.

Handheld scanners must work as a keyboard (HID keyboard wedge) that ends each code with Enter; Tally has
no scanner driver or serial connection. A phone paired from the desktop is the second way to scan: it
reads codes with its camera in the browser and needs no driver, so the phone and browser versions that
passed the §3 check are its record. Printers print through the browser's print dialog, so the
operating system driver of the printer is what is certified with it.

| Kind | Model | Connection | OS and browser | Paper | Report | Result |
|---|---|---|---|---|---|---|
| Barcode scanner | | USB HID | | – | | not run |
| Barcode scanner | | Bluetooth HID | | – | | not run |
| Receipt printer | | USB or network | | 80 mm roll | | not run |
| Document printer | | USB or network | | A5 (sales documents), A4 (statements) | | not run |
