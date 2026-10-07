// Leftover scanner: find every user-visible English string the codemod did NOT
// wrap — dynamic button labels (template literals, ternaries, concatenation),
// hardcoded prose in .ts helpers, aria-labels, helper returns, and so on.
// Non-UI contexts (class names, variants, routes, MIME types, ...) are filtered
// out so the remaining list is small enough to review by hand.
//
//   node i18n-leftovers.mjs
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const UI = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui";
const require = createRequire(UI + "/package.json");
const parser = require("@babel/parser");
const traverse = require("@babel/traverse").default;

const SKIP_FILES = [/[\\/]i18n[\\/]/, /routeTree\.gen\.ts$/, /[\\/]api[\\/]schema\.ts$/, /[\\/]gen[\\/]/];

const CODE_ATTRS = new Set([
  "className", "class", "style", "id", "name", "key", "role", "type", "variant", "size", "color",
  "href", "to", "path", "htmlFor", "method", "target", "rel", "src", "value", "autoComplete",
  "inputMode", "encoding", "as", "slot", "fill", "stroke", "viewBox", "d", "transform", "xmlns",
  "data-testid", "data-test", "data-state", "accept", "pattern", "mode", "orientation",
  "placement", "side", "align", "width", "height", "min", "max", "step", "download",
]);

const CODE_KEYS = new Set([
  "key", "id", "type", "variant", "size", "color", "className", "class", "role", "path", "to",
  "href", "src", "name", "method", "encoding", "format", "status", "kind", "state", "mimeType",
  "accept", "mode", "placement", "side", "align", "as", "slot", "icon", "defaultQueue", "queue",
  "capability", "capabilities", "tag", "tags", "mime", "extension", "preset", "timezone",
  "notificationKind", "severity", "level", "category", "sort", "order", "direction", "theme",
]);

const PRODUCT_NAMES = [
  /\bTeldrive\b/g, /\bTelegram\b/g, /\bRiver\b/g, /\brclone\b/g, /\bAPI\b/g, /\bQR\b/g,
  /\bURL\b/g, /\bID\b/g, /\bE\.164\b/g, /\bMiB\b/g, /\bGiB\b/g, /\bKiB\b/g, /\bBLAKE3\b/g,
  /\bPDF\b/g, /\bEPUB\b/g, /\bHTTP\b/g, /\bJSON\b/g, /\bcron\b/g, /\bPostgreSQL\b/g,
  /\bBotFather\b/g, /\bUTC\b/g, /\bIANA\b/g, /\bCtrl K\b/g,
];

/** Tailwind-ish class strings are not prose. */
function looksLikeClasses(value) {
  if (/[A-Z]/.test(value)) return false;
  const tokens = value.split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return false;
  // A plain lowercase English word means this is prose, not a class list.
  if (tokens.some((token) => /^[a-z]{4,}$/.test(token))) return false;
  return tokens.every((token) => /^[a-z0-9:[\]()/.%#_-]+$/.test(token));
}

/** Pure numbers/units/punctuation carry no language. */
function looksLikeValue(value) {
  return /^[\d\s.,:;/%()+\-–—·]*[A-Za-z]{0,3}[\d\s.,:;/%()+\-–—·]*$/.test(value) && /\d/.test(value);
}

function translatable(text) {
  const value = String(text).replace(/\s+/g, " ").trim();
  if (value.length < 2) return false;
  if (!/[A-Za-z]/.test(value)) return false;
  if (looksLikeClasses(value)) return false;
  if (looksLikeValue(value)) return false;
  let stripped = value;
  for (const re of PRODUCT_NAMES) stripped = stripped.replace(re, " ");
  stripped = stripped.replace(/[\s\d.,:;!?%()\-–—/·&+'"“”…\\|]+/g, "");
  if (stripped.length === 0) return false;
  if (/^[a-z0-9_.:/-]+$/.test(value) && /[._:/-]/.test(value)) return false;
  if (/^[A-Z_]+$/.test(value)) return false;
  if (/^[a-z][a-zA-Z0-9]*$/.test(value)) return false;
  return true;
}

function collect(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules") continue;
      collect(full, out);
    } else if (/\.tsx?$/.test(entry.name) && !SKIP_FILES.some((re) => re.test(full))) {
      out.push(full);
    }
  }
  return out;
}

const files = collect(UI + "/src");
const rows = [];

for (const file of files) {
  const rel = path.relative(UI, file).replace(/\\/g, "/");
  const source = fs.readFileSync(file, "utf8");
  let ast;
  try {
    ast = parser.parse(source, { sourceType: "module", plugins: ["jsx", "typescript"] });
  } catch {
    continue;
  }
  const nodePath = new WeakMap();

  const contextOf = (node) => {
    let p = nodePath.get(node);
    let attr = null;
    let inJsxChild = false;
    let inReturn = false;
    let call = null;
    let keyName = null;
    let depth = 0;
    while (p && depth < 14) {
      if (p.isJSXAttribute?.()) {
        attr = p.node.name?.name ?? "?";
        break;
      }
      if (p.isJSXExpressionContainer?.()) {
        const parent = p.parentPath;
        if (parent?.isJSXElement?.() || parent?.isJSXFragment?.()) inJsxChild = true;
      }
      if (p.isReturnStatement?.()) inReturn = true;
      if (p.isObjectProperty?.() && !keyName) {
        const key = p.node.key;
        keyName = key.type === "Identifier" ? key.name : key.type === "StringLiteral" ? key.value : null;
      }
      if (p.isVariableDeclarator?.() && !keyName && p.node.id.type === "Identifier") {
        keyName = p.node.id.name;
      }
      if (p.isCallExpression?.()) {
        const callee = p.node.callee;
        if (callee.type === "Identifier") {
          call = callee.name;
          break;
        }
      }
      if (p.isNewExpression?.() && p.node.callee.type === "Identifier" && p.node.callee.name === "Error") {
        call = "Error";
        break;
      }
      p = p.parentPath;
      depth += 1;
    }
    const where = attr
      ? `attr:${attr}`
      : inJsxChild
        ? "jsx-child"
        : inReturn
          ? "return"
          : call
            ? `call:${call}`
            : keyName
              ? `key:${keyName}`
              : "other";
    return { attr, inJsxChild, inReturn, call, keyName, where };
  };

  const report = (node, kind, text) => {
    if (!translatable(text)) return;
    const ctx = contextOf(node);
    if (ctx.attr && CODE_ATTRS.has(ctx.attr)) return;
    if (!ctx.attr && ctx.keyName && CODE_KEYS.has(ctx.keyName)) return;
    if (ctx.call === "Error") return;
    if (ctx.where === "other" && kind === "string") return;
    rows.push({
      rel,
      line: node.loc?.start.line,
      kind,
      where: ctx.where,
      text: String(text).replace(/\s+/g, " ").trim().slice(0, 200),
      snippet: source
        .slice(Math.max(0, node.start - 70), Math.min(source.length, node.end + 40))
        .replace(/\s+/g, " ")
        .trim(),
    });
  };

  traverse(ast, {
    enter(p) {
      if (p.node?.type) nodePath.set(p.node, p);
    },
    JSXText(p) {
      const text = p.node.value.replace(/\s+/g, " ").trim();
      if (text) report(p.node, "jsx-text", text);
    },
    JSXAttribute(p) {
      const name = p.node.name?.name;
      if (!name || CODE_ATTRS.has(name)) return;
      const value = p.node.value;
      if (value?.type === "StringLiteral") return report(value, `attr`, value.value);
      if (value?.type === "JSXExpressionContainer") {
        const expr = value.expression;
        if (expr.type === "TemplateLiteral" && expr.quasis.length > 1) {
          report(expr, "attr-template", expr.quasis.map((q) => q.value.cooked ?? "").join(" "));
        }
        if (expr.type === "ConditionalExpression") {
          for (const branch of [expr.consequent, expr.alternate]) {
            if (branch.type === "StringLiteral") report(branch, "attr-ternary", branch.value);
          }
        }
      }
    },
    TemplateLiteral(p) {
      if (p.node.quasis.length < 2) return;
      const text = p.node.quasis.map((q) => q.value.cooked ?? "").join(" ");
      report(p.node, "template", text);
    },
    ConditionalExpression(p) {
      for (const branch of [p.node.consequent, p.node.alternate]) {
        if (branch.type === "StringLiteral") report(branch, "ternary", branch.value);
      }
    },
    BinaryExpression(p) {
      if (p.node.operator !== "+") return;
      for (const side of [p.node.left, p.node.right]) {
        if (side.type === "StringLiteral") report(side, "concat", side.value);
      }
    },
    ReturnStatement(p) {
      const arg = p.node.argument;
      if (!arg) return;
      if (arg.type === "StringLiteral") report(arg, "return", arg.value);
      if (arg.type === "TemplateLiteral" && arg.quasis.length > 1) {
        report(arg, "return-template", arg.quasis.map((q) => q.value.cooked ?? "").join(" "));
      }
      if (arg.type === "ConditionalExpression") {
        for (const branch of [arg.consequent, arg.alternate]) {
          if (branch.type === "StringLiteral") report(branch, "return-ternary", branch.value);
        }
      }
    },
    StringLiteral(p) {
      const parent = p.parent;
      if (
        parent.type === "ImportDeclaration" ||
        parent.type === "ExportNamedDeclaration" ||
        parent.type === "ExportAllDeclaration" ||
        parent.type === "JSXAttribute" ||
        parent.type === "TSLiteralType" ||
        (parent.type === "CallExpression" && parent.callee.type === "Import")
      )
        return;
      report(p.node, "string", p.value);
    },
  });
}

const byWhere = {};
for (const row of rows) byWhere[row.where] = (byWhere[row.where] ?? 0) + 1;
console.log("leftover candidates:", rows.length);
console.log("by context:", JSON.stringify(byWhere, null, 1));
console.log("files:", new Set(rows.map((r) => r.rel)).size);
const out = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/i18n-leftovers.json";
fs.writeFileSync(out, JSON.stringify(rows, null, 1));
console.log("wrote", out);
