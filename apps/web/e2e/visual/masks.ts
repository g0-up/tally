import type { Locator, Page } from "@playwright/test";

// Dynamic text that differs between runs of the same seed: dates and times, document numbers and
// the relative "Quá N ngày" ageing labels. They are masked on both sides of a parity comparison,
// so the screenshots still line up on the layout around them. Matching is by text, because the
// prototype and the app use different class names for the same value.

const DATE = /\b\d{1,2}\/\d{1,2}\/\d{4}\b/;
const TIME = /\b\d{1,2}:\d{2}(:\d{2})?\b/;
const DOCUMENT_NUMBER = /\b[A-Z]{1,4}-[A-Z0-9]{2,}\b/;
const RELATIVE_DAYS = /Quá \d+ ngày/;

/** Elements whose content changes with the clock or with generated numbers. */
export function maskLocators(page: Page): Locator[] {
  return [
    page.locator("time"),
    page.locator("[data-mask]"),
    page.getByText(DATE),
    page.getByText(TIME),
    page.getByText(DOCUMENT_NUMBER),
    page.getByText(RELATIVE_DAYS),
  ];
}
