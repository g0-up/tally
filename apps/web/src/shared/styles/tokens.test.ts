// @vitest-environment node
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The prototype is the visual source of truth: every custom property of its :root must exist in
// tokens.css with the same value.

const PROTOTYPE = new URL("../../../../../docs/design/prototype-v6.14.html", import.meta.url);
const TOKENS = new URL("./tokens.css", import.meta.url);

function rootBlock(css: string): string {
  const start = css.indexOf(":root {");
  if (start < 0) throw new Error("no :root block");
  const end = css.indexOf("}", start);
  return css.slice(start + ":root {".length, end);
}

function customProperties(block: string): Map<string, string> {
  const withoutComments = block.replace(/\/\*[\s\S]*?\*\//g, "");
  const out = new Map<string, string>();
  for (const m of withoutComments.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    const [, name = "", value = ""] = m;
    out.set(name, value.trim().replace(/\s+/g, " "));
  }
  return out;
}

describe("design tokens", () => {
  const prototype = customProperties(rootBlock(readFileSync(PROTOTYPE, "utf8")));
  const tokens = customProperties(rootBlock(readFileSync(TOKENS, "utf8")));

  it("reads the prototype palette", () => {
    expect(prototype.get("--navy-900")).toBe("#0a0d53");
    expect(prototype.size).toBeGreaterThan(40);
  });

  it("defines every prototype :root variable with the same value", () => {
    const mismatches = [...prototype].filter(([name, value]) => tokens.get(name) !== value).map(([name, value]) => `${name}: ${value} != ${tokens.get(name) ?? "(missing)"}`);
    expect(mismatches).toEqual([]);
  });
});

/** WCAG 2.x relative luminance of a #rrggbb color. */
function luminance(hex: string): number {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) throw new Error(`not a #rrggbb color: ${hex}`);
  const [r, g, b] = m.slice(1).map((h) => {
    const c = Number.parseInt(h, 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

/** Straight sRGB mix of two #rrggbb colors, as a gradient renders between its stops. */
function mix(a: string, b: string, t: number): string {
  const channels = (hex: string) => [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16));
  const ca = channels(a);
  const cb = channels(b);
  return `#${ca.map((v, i) => Math.round(v + ((cb[i] ?? 0) - v) * t).toString(16).padStart(2, "0")).join("")}`;
}

describe("contrast of the accessibility tokens", () => {
  const tokens = customProperties(rootBlock(readFileSync(TOKENS, "utf8")));
  const color = (name: string): string => {
    const value = tokens.get(name);
    if (!value) throw new Error(`missing ${name}`);
    return value;
  };

  it.each([
    ["white text on the orange button", "--surface-0", "--accent-orange-strong", 4.5],
    ["white text on the crimson button", "--surface-0", "--accent-crimson-strong", 4.5],
    ["orange series heading on white", "--accent-orange-strong", "--surface-0", 4.5],
    ["placeholder text", "--placeholder", "--surface-0", 4.5],
    ["secondary text", "--muted-strong", "--surface-0", 4.5],
    ["input borders on white", "--input-line", "--surface-0", 3],
    ["input borders on the page background", "--input-line", "--surface-50", 3],
    ["focus ring on the navy sidebar", "--on-dark-focus", "--navy-700", 3],
    ["focus ring on the deep navy header", "--on-dark-focus", "--navy-900", 3],
    ["focus ring on the dark admin header", "--on-dark-focus", "--surface-dark", 3],
    ["button border on the dark admin header", "--on-dark-line", "--surface-dark", 3],
  ])("keeps %s readable", (_what, fg, bg, min) => {
    expect(contrast(color(fg), color(bg))).toBeGreaterThanOrEqual(min);
  });

  it("keeps white text readable across the whole primary button gradient", () => {
    const stops = [...color("--gradient-accent-strong").matchAll(/#[0-9a-f]{6}/gi)].map((m) => m[0]);
    expect(stops).toHaveLength(2);
    const [from = "", to = ""] = stops;
    for (let i = 0; i <= 10; i++) expect(contrast(color("--surface-0"), mix(from, to, i / 10))).toBeGreaterThanOrEqual(4.5);
  });

  it("documents why the stronger tokens exist: the prototype accents fall short", () => {
    expect(contrast(color("--surface-0"), color("--accent-orange"))).toBeLessThan(4.5);
    expect(contrast(color("--surface-0"), color("--accent-crimson"))).toBeLessThan(4.5);
    expect(contrast(color("--gray-300"), color("--surface-0"))).toBeLessThan(3);
  });
});

// jsdom lays nothing out, so the target sizes are checked on the stylesheet's own rules; the real
// boxes are measured by the browser suite.

const GLOBALS = readFileSync(new URL("./globals.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

interface Rule {
  selectors: string[];
  body: string;
}

/** Every `selectors { body }` rule of a stylesheet without nested blocks. */
function rules(css: string): Rule[] {
  return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors = "", body = ""]) => ({ selectors: selectors.split(",").map((s) => s.trim()), body }));
}

/** Inside of the @media block with exactly this query. */
function mediaBlock(query: string): string {
  const start = GLOBALS.indexOf(`@media ${query} {`);
  if (start < 0) throw new Error(`no @media ${query}`);
  let depth = 0;
  for (let i = GLOBALS.indexOf("{", start); i < GLOBALS.length; i++) {
    if (GLOBALS[i] === "{") depth++;
    if (GLOBALS[i] === "}" && --depth === 0) return GLOBALS.slice(GLOBALS.indexOf("{", start) + 1, i);
  }
  throw new Error(`unclosed @media ${query}`);
}

/** Selectors given at least `px` of the property by some rule in `css`. */
function sizedTo(css: string, property: string, px: number): Set<string> {
  const out = new Set<string>();
  for (const r of rules(css)) {
    const m = new RegExp(`(?:^|[;\\s])${property}\\s*:\\s*(\\d+)px`).exec(r.body);
    if (m && Number(m[1]) >= px) r.selectors.forEach((s) => out.add(s));
  }
  return out;
}

const TARGETS = [".lnk", ".btn-sm", ".cp", ".chip", ".x", ".dp-btn", ".bell"];

describe("target size", () => {
  it("gives every small control at least 24 CSS px everywhere", () => {
    // Only rules outside any @media count: they hold at every width.
    const tall = sizedTo(GLOBALS.replace(/@media[^{]+\{(?:[^{}]*\{[^{}]*\})*[^{}]*\}/g, ""), "min-height", 24);
    for (const s of TARGETS) expect(tall, s).toContain(s);
    expect(sizedTo(GLOBALS, "width", 24)).toContain(".sw");
  });

  it("raises the touch targets to 44 CSS px on narrow screens and touch pointers", () => {
    const narrow = mediaBlock("(max-width: 860px), (pointer: coarse)");
    const tall = sizedTo(narrow, "min-height", 44);
    for (const s of [".btn-sm", "button.lnk", ".cp", ".chip", ".x", ".dp-btn", ".bell", ".nav-sub a", ".nav-tg", ".toast .toast-x", ".f input", "select", ".dp-day"]) {
      expect(tall, s).toContain(s);
    }
    for (const s of ["button.lnk", ".x", ".toast .toast-x"]) expect(sizedTo(narrow, "min-width", 44), s).toContain(s);
    expect(sizedTo(narrow, "width", 44)).toContain(".sw");
  });
});
