// Pass 2 codemod: wrap the *dynamic* user-visible strings that pass 1 could not
// handle — ternary branches, template literals with inline values, and hardcoded
// prose returned from helpers (e.g. api/errors.ts userMessage).
//
//   node i18n-codemod2.mjs            # dry run
//   node i18n-codemod2.mjs --write
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const UI = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui";
const WRITE = process.argv.includes("--write");
const require = createRequire(UI + "/package.json");
const parser = require("@babel/parser");
const traverse = require("@babel/traverse").default;

const ATTRS = new Set([
  "label", "title", "description", "placeholder", "aria-label", "aria-description", "alt",
  "header", "tooltip", "emptyContent", "emptyText", "emptyTitle", "emptyHint", "message",
  "confirmText", "cancelText", "textValue", "subtitle", "helperText", "errorMessage",
  "loadingText", "detail", "confirmLabel", "rootLabel", "downloadName",
]);

const SKIP_FILES = [/[\\/]i18n[\\/]/, /routeTree\.gen\.ts$/, /[\\/]api[\\/]schema\.ts$/, /[\\/]gen[\\/]/];

// Elements whose text is technical content rather than prose.
const SKIP_ELEMENTS = new Set(["kbd", "Kbd", "code", "pre", "samp", "script", "style", "textarea"]);

function decodeEntities(value) {
  return value
    .replace(/&nbsp;/g, " ")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&middot;/g, "·");
}

/** CSS/URL/SQL snippets must never be translated. */
function looksLikeCode(value) {
  return (
    value.includes(";") ||
    value.includes("@namespace") ||
    value.includes("color-scheme") ||
    value.includes("!important") ||
    value.includes("://") ||
    value.includes("{") ||
    value.includes("}") ||
    /(^|\s)(select|insert|update|delete|from)\s/i.test(value)
  );
}

const PRODUCT_NAMES = [
  /\bTeldrive\b/g, /\bTelegram\b/g, /\bRiver\b/g, /\brclone\b/g, /\bAPI\b/g, /\bQR\b/g,
  /\bURL\b/g, /\bID\b/g, /\bE\.164\b/g, /\bMiB\b/g, /\bGiB\b/g, /\bKiB\b/g, /\bBLAKE3\b/g,
  /\bPDF\b/g, /\bEPUB\b/g, /\bHTTP\b/g, /\bJSON\b/g, /\bcron\b/g, /\bPostgreSQL\b/g,
  /\bBotFather\b/g, /\bUTC\b/g, /\bIANA\b/g, /\bCtrl K\b/g,
];

function looksLikeClasses(value) {
  if (/[A-Z]/.test(value)) return false;
  const tokens = value.split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return false;
  // A plain lowercase English word means this is prose, not a class list.
  if (tokens.some((token) => /^[a-z]{4,}$/.test(token))) return false;
  return tokens.every((token) => /^[a-z0-9:[\]()/.%#_-]+$/.test(token));
}

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

/** Does this expression subtree contain a ternary (needs human wording)? */
function hasConditional(node) {
  if (!node || typeof node.type !== "string") return false;
  if (node.type === "ConditionalExpression") return true;
  for (const key of Object.keys(node)) {
    if (["loc", "start", "end", "leadingComments", "trailingComments", "extra"].includes(key)) continue;
    const child = node[key];
    if (Array.isArray(child)) {
      if (child.some((item) => hasConditional(item))) return true;
    } else if (child && typeof child.type === "string" && hasConditional(child)) {
      return true;
    }
  }
  return false;
}

const files = collect(UI + "/src");
const keys = new Set();
const editsByFile = new Map();
const skipped = [];
const stats = { attrTemplate: 0, attrTernary: 0, childTernary: 0, childTemplate: 0, ret: 0, retTemplate: 0, retTernary: 0 };

for (const file of files) {
  const rel = path.relative(UI, file).replace(/\\/g, "/");
  const source = fs.readFileSync(file, "utf8");
  let ast;
  try {
    ast = parser.parse(source, { sourceType: "module", plugins: ["jsx", "typescript"] });
  } catch (error) {
    console.log("PARSE FAIL", rel, error.message);
    continue;
  }

  let identifier = "t";
  let programPath = null;
  traverse(ast, {
    Program(p) {
      programPath = p;
      p.stop();
    },
  });
  // Reuse the identifier of an existing i18n import (pass 1 usually added one);
  // only fall back to `translate` when a real module binding named `t` exists.
  const existingImport = /import\s*\{\s*([A-Za-z_$][\w$]*)\s*\}\s*from\s*"@\/i18n"/.exec(source);
  if (existingImport) identifier = existingImport[1];
  else if (programPath?.scope?.hasBinding("t")) identifier = "translate";

  const add = (start, end, replacement, key) => {
    if (!editsByFile.has(file)) editsByFile.set(file, []);
    editsByFile.get(file).push({ start, end, replacement });
    keys.add(key);
  };

  /** Build t("...", {a, b}) from a template literal, or null when unsupported. */
  const templateCall = (node) => {
    const parts = [];
    const vars = new Map();
    for (let i = 0; i < node.quasis.length; i += 1) {
      parts.push((node.quasis[i].value.cooked ?? "").replace(/\s+/g, " "));
      const expr = node.expressions[i];
      if (!expr) continue;
      let name = null;
      let value = null;
      if (expr.type === "Identifier") {
        name = expr.name;
        value = expr.name;
      } else if (expr.type === "MemberExpression" && !expr.computed) {
        const text = source.slice(expr.start, expr.end);
        if (!/^[A-Za-z_$][\w$]*(?:\??\.[A-Za-z_$][\w$]*)*$/.test(text)) return null;
        const last = expr.property.type === "Identifier" ? expr.property.name : null;
        if (!last) return null;
        name = last;
        value = text;
      } else if (expr.type === "StringLiteral") {
        parts.push(expr.value);
        continue;
      } else {
        // Arbitrary inline expression (e.g. `index + 1`, `job?.id ?? ""`): pass it
        // through as a positional placeholder. Nested ternaries are skipped
        // because they need human wording, not a machine placeholder.
        const raw = source.slice(expr.start, expr.end);
        if (raw.length > 160 || raw.includes("`") || raw.includes("<") || hasConditional(expr)) return null;
        name = `value${vars.size}`;
        value = raw;
      }
      vars.set(name, value);
      parts.push(`{{${name}}}`);
    }
    const template = parts.join("").replace(/\s+/g, " ").trim();
    const rawText = node.quasis.map((q) => q.value.cooked ?? "").join(" ");
    if (looksLikeCode(rawText)) return null;
    if (!translatable(template.replace(/\{\{[^}]+\}\}/g, " "))) return null;
    if (!template.includes("{{")) return null;
    const args = [...vars.entries()].map(([name, value]) => (name === value ? name : `${name}: ${value}`));
    return { key: template, call: `${identifier}(${JSON.stringify(template)}, { ${args.join(", ")} })` };
  };

  /** Wrap the string branches of every conditional inside an expression. */
  const wrapConditionals = (node) => {
    const visit = (n) => {
      if (!n || typeof n.type !== "string") return;
      if (
        n.type === "JSXElement" ||
        n.type === "JSXFragment" ||
        n.type === "FunctionExpression" ||
        n.type === "ArrowFunctionExpression" ||
        n.type === "FunctionDeclaration"
      )
        return;
      if (n.type === "ConditionalExpression") {
        for (const branch of [n.consequent, n.alternate]) {
          if (branch.type === "StringLiteral" && translatable(branch.value)) {
            add(branch.start, branch.end, `${identifier}(${JSON.stringify(branch.value)})`, branch.value);
            stats.ternary = (stats.ternary ?? 0) + 1;
          } else {
            visit(branch);
          }
        }
        return;
      }
      for (const key of Object.keys(n)) {
        if (["loc", "start", "end", "leadingComments", "trailingComments", "extra"].includes(key)) continue;
        const child = n[key];
        if (Array.isArray(child)) {
          for (const item of child) visit(item);
        } else if (child && typeof child.type === "string") {
          visit(child);
        }
      }
    };
    visit(node);
  };

  traverse(ast, {
    JSXText(p) {
      const raw = p.node.value;
      const inner = raw.trim();
      if (!translatable(decodeEntities(inner))) return;
      const parent = p.parent;
      if (parent?.type === "JSXElement") {
        const name = parent.openingElement.name;
        if (name.type === "JSXIdentifier" && SKIP_ELEMENTS.has(name.name)) return;
      }
      const leading = raw.slice(0, raw.length - raw.trimStart().length);
      const trailing = raw.slice(raw.trimEnd().length);
      const text = decodeEntities(inner.replace(/\s+/g, " "));
      add(p.node.start, p.node.end, `${leading}{${identifier}(${JSON.stringify(text)})}${trailing}`, text);
      stats.jsxText = (stats.jsxText ?? 0) + 1;
    },

    CallExpression(p) {
      // UI helpers such as toast.error(...) / setError(...) / performAction(...)
      // carry inline ternaries and template literals.
      for (const arg of p.node.arguments) {
        if (arg.type === "TemplateLiteral") {
          const built = templateCall(arg);
          if (built) {
            add(arg.start, arg.end, built.call, built.key);
            stats.callTemplate = (stats.callTemplate ?? 0) + 1;
          }
          continue;
        }
        wrapConditionals(arg);
      }
    },

    JSXAttribute(p) {
      const name = p.node.name?.name;
      if (!name || !ATTRS.has(name)) return;
      const value = p.node.value;
      if (value?.type !== "JSXExpressionContainer") return;
      const expr = value.expression;
      if (expr.type === "TemplateLiteral") {
        const built = templateCall(expr);
        if (built) {
          add(expr.start, expr.end, built.call, built.key);
          stats.attrTemplate += 1;
        }
        return;
      }
      if (expr.type === "ConditionalExpression") {
        wrapConditionals(expr);
      }
    },

    JSXExpressionContainer(p) {
      const parent = p.parent;
      if (parent?.type !== "JSXElement" && parent?.type !== "JSXFragment") return;
      const expr = p.node.expression;
      if (expr.type === "TemplateLiteral" || expr.type === "ConditionalExpression") {
        if (expr.type === "TemplateLiteral") {
          const built = templateCall(expr);
          if (built) {
            add(expr.start, expr.end, built.call, built.key);
            stats.childTemplate += 1;
          }
        } else {
          wrapConditionals(expr);
        }
        return;
      }
    },

    ReturnStatement(p) {
      const arg = p.node.argument;
      if (!arg) return;
      if (arg.type === "StringLiteral") {
        if (!translatable(arg.value)) return;
        add(arg.start, arg.end, `${identifier}(${JSON.stringify(arg.value)})`, arg.value);
        stats.ret += 1;
        return;
      }
      if (arg.type === "TemplateLiteral") {
        const built = templateCall(arg);
        if (built) {
          add(arg.start, arg.end, built.call, built.key);
          stats.retTemplate += 1;
        }
        return;
      }
      if (arg.type === "ConditionalExpression") {
        wrapConditionals(arg);
      }
    },
  });
}

const broken = [];
let written = 0;
for (const [file, edits] of editsByFile) {
  const rel = path.relative(UI, file).replace(/\\/g, "/");
  if (!WRITE) continue;
  const source = fs.readFileSync(file, "utf8");
  const sorted = [...edits].sort((a, b) => b.start - a.start);
  let output = source;
  for (const edit of sorted) {
    if (output.slice(edit.start, edit.end) === edit.replacement) continue;
    output = output.slice(0, edit.start) + edit.replacement + output.slice(edit.end);
  }
  let identifier = "t";
  const existingImport = /import\s*\{\s*([A-Za-z_$][\w$]*)\s*\}\s*from\s*"@\/i18n"/.exec(source);
  if (existingImport) identifier = existingImport[1];
  else if (/(?:^|\n)\s*(?:const|let|var|function)\s+t\b/.test(source)) identifier = "translate";
  const needsImport = !/from\s+"@\/i18n"/.test(source);
  if (needsImport) {
    const importLine = `import { ${identifier} } from "@/i18n";`;
    const importRe = /^import[\s\S]*?from\s+["'][^"']+["'];?[ \t]*$/gm;
    let last = null;
    let match;
    while ((match = importRe.exec(output)) !== null) last = match;
    if (last) {
      const end = last.index + last[0].length;
      output = `${output.slice(0, end)}\n${importLine}${output.slice(end)}`;
    } else {
      output = `${importLine}\n${output}`;
    }
  }
  try {
    parser.parse(output, { sourceType: "module", plugins: ["jsx", "typescript"] });
  } catch (error) {
    broken.push(`${rel}: ${error.message.split("\n")[0]}`);
    continue;
  }
  fs.writeFileSync(file, output);
  written += 1;
}

console.log(`mode: ${WRITE ? "WRITE" : "dry run"}`);
console.log("wrap sites:", JSON.stringify(stats));
console.log("unique keys:", keys.size, " files touched:", editsByFile.size, " written:", written);
if (broken.length) {
  console.log("!! skipped (would not parse):");
  for (const row of broken) console.log("   ", row);
}
fs.writeFileSync(
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/i18n-keys2.json",
  JSON.stringify([...keys].sort(), null, 1),
);
console.log("wrote tools/i18n-keys2.json");
