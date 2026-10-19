// @vitest-environment node
import { RuleTester } from "eslint";
import tseslint from "typescript-eslint";
import { noRawColorPlugin } from "./no-raw-color.js";

const tester = new RuleTester({
  languageOptions: {
    parser: tseslint.parser,
    parserOptions: { ecmaFeatures: { jsx: true } },
  },
});

tester.run("no-raw-color", noRawColorPlugin.rules["no-raw-color"], {
  valid: [
    { code: 'const c = "bg-navy-700 text-surface-0";' },
    { code: 'const s = { color: "var(--navy-900)" };' },
    { code: 'const h = "#ban-hang.pos";' },
    { code: 'const e = <span className="text-[13px] px-[18px]">#1 trong tuần</span>;' },
    { code: "const t = `w-[${width}px]`;" },
  ],
  invalid: [
    { code: 'const c = "#f45d29";', errors: [{ messageId: "raw" }] },
    { code: 'const c = { background: "rgba(0, 26, 45, .45)" };', errors: [{ messageId: "raw" }] },
    { code: 'const e = <div className="bg-[#0a0d53]" />;', errors: [{ messageId: "arbitrary" }] },
    { code: 'const e = <div className="text-[rgb(0,0,0)]" />;', errors: [{ messageId: "arbitrary" }] },
    { code: "const t = `border-[color:var(--x)] ${a}`;", errors: [{ messageId: "arbitrary" }] },
    { code: 'const e = <i style={{ background: "hsl(10 20% 30%)" }} />;', errors: [{ messageId: "raw" }] },
  ],
});
