---
phase: 2
title: "Frame: top bar, page head, rail, step card"
status: completed
effort: 1d
---

# Phase 2 — Frame: top bar, page head, rail, step card

## Context

Prototype `renderOnb`, `obRail`, `obCard`, `obGate`, `obLocked`, `obEmpty`, `onbGoTo`, the Escape handler and `A.onbRail/onbSkip/onbEnter/onbLogout`. Current: `onboarding-layout.tsx` (brand only), `onboarding-page.tsx` (Stepper + checklist card + StepBody), `components/step-card.tsx`, `onboarding-checklist.tsx`, `opening-gate.tsx`.

## Requirements

Top bar (`OnboardingLayout`):
- `header.onb-top` sticky: `.onb-brand` with `<b>{BRAND_NAME}</b><span>{BRAND_TAGLINE}</span>`, `.sp`, `.who` = "{display name} · " (when set) + "Chủ cửa hàng **{owner name}**", "Đăng xuất" button: `<LogoutButton className="onb-out" />` (it already accepts `className`).
- `main.view#onb-main` focusable; keep skip-link target behaviour consistent with the app shell.

Page (`OnboardingPage`):
- `PageHeader` eyebrow "Bắt đầu", title "Thiết lập cửa hàng", lead "Làm 5 bước bắt buộc để mở bán. Bước tùy chọn có thể bỏ qua và làm sau." (count from data), actions = `.onb-prog` block: `.k#onb-prog-k` "Bước bắt buộc", `<b class="tab-n">{d}<small> / {n} đã xong</small></b>`, `.meter.acc` progressbar with `aria-labelledby`, `aria-valuemax=n`, `aria-valuenow=d`, `aria-valuetext="{d} trên {n} bước bắt buộc đã xong"` (the shared `Progress` uses percent; render the prototype markup directly or extend `Progress` with `valueText`/raw max).
- Ready banner `section.card.onb-ready` when every required step is completed: h2 "Cửa hàng đã sẵn sàng bán", p "Các bước bắt buộc đã xong và số dư đầu kỳ đã khóa đến hết ngày {cutoff}. Bán đơn đầu tiên ngay hoặc vào màn chính.", `btn-primary` "Vào cửa hàng" → `HOME_PATH`.
- `.onb-grid` = rail + card.

Rail (`components/step-rail.tsx`, new; replaces `onboarding-checklist.tsx` and the `Stepper`):
- `nav.onb-rail[aria-label="Các bước thiết lập"]` + `.open` state; toggle button `.onb-rail-tg` (`aria-expanded`, `aria-controls="onb-groups"`) "Bước **n/7** · {label}" + chevron svg.
- Per phase: `.onb-ph#onb-ph-{i}` with sub `Tính đến hết ngày {cutoff}` / `Cần chọn ngày chốt trước` (phase 1), `Sau khi khóa số dư` (phase 2); `ol.onb-steps[aria-labelledby]`.
- Step button `.onb-step` + `done|skip|locked`, `aria-current="step"` on the open one, `.onb-mk` (check svg when completed, else number), sr-only "Bước i: ", `<b>` label, `<small>` (Đã xong[ · lock] / Đã bỏ qua / lock / Bắt buộc / Tùy chọn).
- Escape closes the open rail and returns focus to the toggle; choosing a step closes it.

Step card (`components/step-card.tsx`, rewritten as `StepFrame`):
- `section.card.onb-card[aria-labelledby="onb-h"]`; `.onb-sh`: eyebrow "Bước i/7 · {phase}", `h2#onb-h[tabindex=-1]`, note; right side `.btn-row` with tag "Bắt buộc" / tag alt "Tùy chọn" + status chip.
- `.onb-body` = children; `.onb-foot`: "← Bước trước" (`btn-line btn-sm`), right `.btn-row.onb-nav` with "Bỏ qua bước này" (`.lnk`, optional pending steps; `useStepCommand(key,"skip")`, toast "Đã bỏ qua **{label}**. Có thể làm lại bất cứ lúc nào.", then go to next) and "Bước tiếp →".
- Expose `cta` variant (`"primary" | "navy"`) via context or prop so step bodies follow the one-CTA rule.
- Step change: set step, close rail, focus `#onb-h` with `preventScroll`, scroll the card into view when its top is < 70px or > 60% of the viewport (prototype `onbGoTo`).
- Step bodies throw → `Callout tone="bad"` "Không hiển thị được bước này: …" (an error boundary around the body).

Shared helpers (`components/opening-gate.tsx`, rewritten):
- Gate callout warn: "**Cần ngày chốt số dư trước.** Chọn ngày chốt ở bước Thông tin cửa hàng, số dư bước này tính đến hết ngày đó." + `.lnk` "Đến Thông tin cửa hàng".
- Locked callout ok: "**Đã khóa sổ đến hết ngày {cutoff}.** Số dư đầu kỳ không sửa được nữa. Sai lệch sau ngày chốt được xử lý bằng chứng từ điều chỉnh."
- `OnbEmpty` (`.onb-empty`): bold title, text, optional centered actions.
- `useOnbFail()`: sets field errors, focuses the first one, toasts "**Chưa lưu.** Còn {n} chỗ cần sửa, đã đưa con trỏ tới chỗ đầu tiên." (tone err). Reuse `useFocusFirstInvalid`.

Opened step: first pending in display order on first load (keep "chosen once" behaviour); steps call `goTo(next)` after their primary action as the prototype does.

## Files

- Modify `app/layouts/onboarding-layout.tsx`, `features/onboarding/pages/onboarding-page.tsx`, `components/step-card.tsx`, `components/opening-gate.tsx`.
- Create `components/step-rail.tsx`, `components/onb-progress.tsx` (or extend `shared/ui/progress.tsx`).
- Delete `components/onboarding-checklist.tsx` once unused.

## Validation

`pnpm typecheck`, `pnpm lint`; render the page against the MSW handlers and compare with prototype `#onboarding` at 1440 and 390 widths.

## Risk

The page header `.acts` slot must lay out the progress block like `pageHead(..., prog)`; check `.ph .acts` CSS wraps on mobile (`.onb-prog { width:100% }`).
