// Scan the UI source for translatable strings so we can size the i18n job.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const UI = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui";
const require = createRequire(UI + "/package.json");
const parser = require("@babel/parser");
const traverse = require("@babel/traverse").default;

const ATTRS = new Set([
  "label", "title", "description", "placeholder", "aria-label", "alt", "header",
  "tooltip", "emptyContent", "message", "confirmText", "cancelText", "text",
]);

const files = [];
(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else if (/\.tsx?$/.test(e.name)) files.push(p);
  }
})(UI + "/src");

const jsxText = [];
const mixed = [];
const attrs = [];
const callStrings = [];

for (const file of files) {
  const rel = path.relative(UI, file).replace(/\\/g, "/");
  let ast;
  try {
    ast = parser.parse(fs.readFileSync(file, "utf8"), {
      sourceType: "module",
      plugins: ["jsx", "typescript"],
      errorRecovery: true,
    });
  } catch (e) {
    console.log("PARSE FAIL", rel, e.message);
    continue;
  }
  traverse(ast, {
    JSXText(p) {
      const raw = p.node.value;
      const text = raw.replace(/\s+/g, " ").trim();
      if (!/[A-Za-z\u4e00-\u9fff]/.test(text)) return;
      if (text.length < 2) return;
      const parent = p.parent;
      const kids = parent.children ?? [];
      const hasExpr = kids.some((k) => k.type === "JSXExpressionContainer" && k.expression.type !== "JSXEmptyExpression");
      (hasExpr ? mixed : jsxText).push({ rel, text, line: p.node.loc?.start.line });
    },
    JSXAttribute(p) {
      const name = p.node.name?.name;
      if (!name || !ATTRS.has(name)) return;
      if (p.node.value?.type !== "StringLiteral") return;
      const text = p.node.value.value.trim();
      if (!/[A-Za-z]/.test(text)) return;
      attrs.push({ rel, attr: name, text, line: p.node.loc?.start.line });
    },
    CallExpression(p) {
      const callee = p.node.callee;
      const isToast =
        callee.type === "MemberExpression" &&
        callee.object.type === "Identifier" &&
        callee.object.name === "toast";
      if (!isToast) return;
      for (const arg of p.node.arguments) {
        if (arg.type === "StringLiteral" && /[A-Za-z]/.test(arg.value))
          callStrings.push({ rel, text: arg.value, line: arg.loc?.start.line });
        if (arg.type === "ObjectExpression") {
          for (const prop of arg.properties) {
            if (prop.type === "ObjectProperty" && prop.value.type === "StringLiteral")
              callStrings.push({ rel, text: prop.value.value, line: prop.value.loc?.start.line });
          }
        }
      }
    },
  });
}

const uniq = (arr, key) => new Set(arr.map((x) => x[key]));
console.log("files scanned          :", files.length);
console.log("pure JSX text strings  :", jsxText.length, " unique:", uniq(jsxText, "text").size);
console.log("mixed text+expr (manual):", mixed.length, " unique:", uniq(mixed, "text").size);
console.log("attribute strings      :", attrs.length, " unique:", uniq(attrs, "text").size);
console.log("toast strings          :", callStrings.length, " unique:", uniq(callStrings, "text").size);
const allUnique = new Set([
  ...jsxText.map((x) => x.text),
  ...attrs.map((x) => x.text),
  ...callStrings.map((x) => x.text),
  ...mixed.map((x) => x.text),
]);
console.log("TOTAL unique candidate :", allUnique.size);

fs.writeFileSync(
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/i18n-scan.json",
  JSON.stringify({ jsxText, mixed, attrs, callStrings }, null, 1),
);
console.log("\n-- sample pure JSX text --");
for (const s of jsxText.slice(0, 25)) console.log("  ", s.rel + ":" + s.line, JSON.stringify(s.text));
console.log("\n-- sample mixed --");
for (const s of mixed.slice(0, 15)) console.log("  ", s.rel + ":" + s.line, JSON.stringify(s.text));
console.log("\n-- files by candidate count --");
const byFile = {};
for (const s of [...jsxText, ...attrs, ...callStrings, ...mixed]) byFile[s.rel] = (byFile[s.rel] ?? 0) + 1;
Object.entries(byFile)
  .sort((a, b) => b[1] - a[1])
  .slice(0, 25)
  .forEach(([f, n]) => console.log(`  ${String(n).padStart(3)}  ${f}`));
