// Local ESLint plugin: feature code must take every color from the design tokens.
// Flags raw color literals (hex, rgb/rgba, hsl/hsla, hwb, lab/lch, oklab/oklch) and Tailwind
// arbitrary color values such as `bg-[#fff]` or `text-[rgb(0,0,0)]` in string and template
// literals. Token-based classes (`bg-navy-700`) and CSS variables (`var(--navy-700)`) pass.

const HEX = /(^|[^\w&#-])#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})(?![\w-])/;
const COLOR_FN = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\s*\(/i;
const ARBITRARY_COLOR =
  /\b(?:bg|text|border(?:-[trblxy])?|ring|ring-offset|outline|fill|stroke|from|via|to|accent|caret|decoration|divide|placeholder|shadow)-\[(?:color:|#|(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch)\()/i;

function findViolation(text) {
  if (ARBITRARY_COLOR.test(text)) return "arbitrary";
  if (HEX.test(text) || COLOR_FN.test(text)) return "raw";
  return null;
}

const rule = {
  meta: {
    type: "problem",
    docs: { description: "Disallow raw color values and arbitrary Tailwind colors; use design tokens." },
    schema: [],
    messages: {
      raw: "Raw color value \"{{value}}\". Use a design token (CSS variable or token class) instead.",
      arbitrary: "Arbitrary Tailwind color \"{{value}}\". Use a token class such as `bg-navy-700`.",
    },
  },
  create(context) {
    const check = (node, text) => {
      const kind = findViolation(text);
      if (kind) {
        context.report({ node, messageId: kind, data: { value: text.length > 60 ? `${text.slice(0, 57)}...` : text } });
      }
    };
    return {
      Literal(node) {
        if (typeof node.value === "string") check(node, node.value);
      },
      TemplateElement(node) {
        check(node, node.value.cooked ?? node.value.raw);
      },
    };
  },
};

export const noRawColorPlugin = {
  meta: { name: "tally-design-tokens" },
  rules: { "no-raw-color": rule },
};

export { findViolation };
