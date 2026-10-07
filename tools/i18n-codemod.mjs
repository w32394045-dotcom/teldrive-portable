// AST codemod: wrap user-visible English strings in t("...") and collect the
// message catalog (English source text doubles as the key).
//
//   node i18n-codemod.mjs            # dry run: report only
//   node i18n-codemod.mjs --write    # apply edits in place
//
// Only whole-string JSX text, a whitelist of user-facing JSX attributes, toast
// notifications and in-function object properties are touched. Everything else
// (class names, ids, routes, test ids, code samples) is left alone.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const UI = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui";
const WRITE = process.argv.includes("--write");
const require = createRequire(UI + "/package.json");
const parser = require("@babel/parser");
const traverse = require("@babel/traverse").default;

const ATTRS = new Set([
  "label",
  "title",
  "description",
  "placeholder",
  "aria-label",
  "aria-description",
  "alt",
  "header",
  "tooltip",
  "emptyContent",
  "emptyText",
  "message",
  "confirmText",
  "cancelText",
  "textValue",
  "subtitle",
  "helperText",
  "errorMessage",
  "loadingText",
]);

const OBJ_KEYS = new Set(["label", "title", "description", "textValue", "placeholder", "emptyText"]);

// Elements whose text is technical content rather than prose.
const SKIP_ELEMENTS = new Set(["kbd", "Kbd", "code", "pre", "samp", "script", "style", "textarea"]);

const SKIP_FILES = [
  /[\\/]i18n[\\/]/,
  /routeTree\.gen\.ts$/,
  /[\\/]api[\\/]schema\.ts$/,
  /[\\/]gen[\\/]/,
];

const PRODUCT_NAMES = [/\bTeldrive\b/g, /\bTelegram\b/g, /\bRiver\b/g, /\brclone\b/g, /\bAPI\b/g, /\bQR\b/g, /\bURL\b/g, /\bID\b/g, /\bE\.164\b/g, /\bMiB\b/g, /\bGiB\b/g, /\bKiB\b/g, /\bBLAKE3\b/g, /\bPDF\b/g, /\bEPUB\b/g, /\bHTTP\b/g];

function translatable(text) {
  const value = text.replace(/\s+/g, " ").trim();
  if (value.length < 2) return false;
  if (!/[A-Za-z]/.test(value)) return false;
  // Skip strings that are only product names / units / punctuation.
  let stripped = value;
  for (const re of PRODUCT_NAMES) stripped = stripped.replace(re, " ");
  stripped = stripped.replace(/[\s\d.,:;!?%()\-–—/·&+'"“”…]+/g, "");
  if (stripped.length === 0) return false;
  // Skip file-ish or identifier-ish strings.
  if (/^[a-z0-9_.-]+$/.test(value) && /[._/-]/.test(value)) return false;
  return true;
}

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
const keys = new Set();
const editsByFile = new Map();
const manual = [];
const stats = { jsxText: 0, attr: 0, toast: 0, object: 0, template: 0 };

function addEdit(file, start, end, replacement, key) {
  if (!editsByFile.has(file)) editsByFile.set(file, []);
  editsByFile.get(file).push({ start, end, replacement });
  keys.add(key);
}

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

  // Pick an identifier that is not already bound in this module.
  let identifier = "t";
  let programPath = null;
  traverse(ast, {
    Program(p) {
      programPath = p;
      p.stop();
    },
  });
  if (programPath) {
    const scope = programPath.scope;
    if (scope.hasBinding("t") || scope.hasGlobal("t")) identifier = "translate";
  }

  traverse(ast, {
    JSXText(p) {
      const raw = p.node.value;
      const inner = raw.trim();
      if (!translatable(decodeEntities(inner))) return;
      const parent = p.parent;
      if (parent?.type === "JSXElement") {
        const name = parent.openingElement.name;
        if (name.type === "JSXIdentifier" && SKIP_ELEMENTS.has(name.name)) return;
        // Flat prose with inline values is owned by the JSXElement visitor
        // below; wrapping the text here as well would create overlapping edits.
        const siblings = parent.children ?? [];
        const hasExpr = siblings.some(
          (c) => c.type === "JSXExpressionContainer" && c.expression.type !== "JSXEmptyExpression",
        );
        const hasJsxChild = siblings.some(
          (c) => c.type === "JSXElement" || c.type === "JSXFragment",
        );
        if (hasExpr && !hasJsxChild) return;
      }
      const leading = raw.slice(0, raw.length - raw.trimStart().length);
      const trailing = raw.slice(raw.trimEnd().length);
      const text = decodeEntities(inner.replace(/\s+/g, " "));
      addEdit(
        file,
        p.node.start,
        p.node.end,
        `${leading}{${identifier}(${JSON.stringify(text)})}${trailing}`,
        text,
      );
      stats.jsxText += 1;
    },

    JSXAttribute(p) {
      const name = p.node.name?.name;
      if (!name || !ATTRS.has(name)) return;
      if (p.node.value?.type !== "StringLiteral") return;
      const text = p.node.value.value.trim();
      if (!translatable(text)) return;
      addEdit(file, p.node.value.start, p.node.value.end, `{${identifier}(${JSON.stringify(text)})}`, text);
      stats.attr += 1;
    },

    CallExpression(p) {
      const callee = p.node.callee;
      const isToast =
        callee.type === "MemberExpression" &&
        callee.object.type === "Identifier" &&
        callee.object.name === "toast";
      if (!isToast) return;
      for (const arg of p.node.arguments) {
        if (arg.type === "StringLiteral" && translatable(arg.value)) {
          addEdit(file, arg.start, arg.end, `${identifier}(${JSON.stringify(arg.value)})`, arg.value);
          stats.toast += 1;
        } else if (arg.type === "ObjectExpression") {
          for (const prop of arg.properties) {
            if (
              prop.type === "ObjectProperty" &&
              prop.value.type === "StringLiteral" &&
              translatable(prop.value.value)
            ) {
              addEdit(
                file,
                prop.value.start,
                prop.value.end,
                `${identifier}(${JSON.stringify(prop.value.value)})`,
                prop.value.value,
              );
              stats.toast += 1;
            }
          }
        }
      }
    },

    ObjectProperty(p) {
      const key = p.node.key;
      const name =
        key.type === "Identifier" ? key.name : key.type === "StringLiteral" ? key.value : undefined;
      if (!name || !OBJ_KEYS.has(name)) return;
      if (p.node.value.type !== "StringLiteral") return;
      const text = p.node.value.value.trim();
      if (!translatable(text)) return;
      const stack = p.getAncestry();
      const inFunction = stack.some((node) =>
        ["FunctionDeclaration", "FunctionExpression", "ArrowFunctionExpression"].includes(node.type),
      );
      if (!inFunction) {
        manual.push({ rel, line: p.node.loc?.start.line, text, reason: "module-level object" });
        return;
      }
      addEdit(file, p.node.value.start, p.node.value.end, `${identifier}(${JSON.stringify(text)})`, text);
      stats.object += 1;
    },

    JSXElement(p) {
      const children = p.node.children;
      const texts = children.filter((c) => c.type === "JSXText");
      const exprs = children.filter(
        (c) => c.type === "JSXExpressionContainer" && c.expression.type !== "JSXEmptyExpression",
      );
      if (exprs.length === 0 || texts.length === 0) return;
      // Only handle flat prose: text plus simple value expressions, no JSX
      // children and no control flow.
      const hasJsx = children.some((c) => c.type === "JSXElement" || c.type === "JSXFragment");
      if (hasJsx) return;
      const parts = [];
      const vars = {};
      let ok = true;
      for (const child of children) {
        if (child.type === "JSXText") {
          parts.push(decodeEntities(child.value.replace(/\s+/g, " ")));
          continue;
        }
        if (child.type === "JSXExpressionContainer") {
          const expr = child.expression;
          if (expr.type === "StringLiteral") {
            parts.push(expr.value);
            continue;
          }
          if (expr.type === "Identifier") {
            vars[expr.name] = expr.name;
            parts.push(`{{${expr.name}}}`);
            continue;
          }
          if (expr.type === "MemberExpression" && expr.property.type === "Identifier") {
            const name = expr.property.name;
            vars[name] = null; // filled from source text below
            parts.push(`{{${name}}}`);
            continue;
          }
          ok = false;
          break;
        }
        // Comments etc. contribute nothing.
      }
      if (!ok) return;
      const template = parts.join("").replace(/\s+/g, " ").trim();
      if (!translatable(template.replace(/\{\{[^}]+\}\}/g, " "))) return;
      if (!template.includes("{{")) return;
      const args = [];
      for (const [name, binding] of Object.entries(vars)) {
        if (binding) {
          args.push(name);
          continue;
        }
        // Recover `<expr>.property` source text so we can pass the value inline.
        const raw = source.slice(p.node.start, p.node.end);
        const match = new RegExp(`\\{\\s*([A-Za-z_$][\\w$]*(?:\\??\\.[A-Za-z_$][\\w$]*)*\\.${name})\\s*\\}`).exec(raw);
        if (!match) {
          ok = false;
          break;
        }
        vars[name] = match[1];
        args.push(`${name}: ${match[1]}`);
      }
      if (!ok) return;
      const call =
        args.length > 0
          ? `${identifier}(${JSON.stringify(template)}, { ${args.join(", ")} })`
          : `${identifier}(${JSON.stringify(template)})`;
      // Replace only the element's children, never the element (tags, props and
      // layout classes must stay exactly where they are).
      const first = children[0];
      const last = children[children.length - 1];
      addEdit(file, first.start, last.end, `{${call}}`, template);
      stats.template += 1;
    },
  });

  // Insert the import and use the chosen identifier throughout this file.
  const edits = editsByFile.get(file) ?? [];
  if (edits.length > 0 && identifier !== "t") {
    for (const edit of edits) {
      edit.replacement = edit.replacement.replaceAll("t(", "translate(");
      edit.replacement = edit.replacement.replaceAll("t .", "translate .");
    }
  }
}

// Report / apply
const summary = [];
const broken = [];
for (const [file, edits] of editsByFile) {
  const rel = path.relative(UI, file).replace(/\\/g, "/");
  summary.push({ rel, count: edits.length });
  if (!WRITE) continue;
  const source = fs.readFileSync(file, "utf8");
  const sorted = [...edits].sort((a, b) => b.start - a.start);
  let output = source;
  for (const edit of sorted) {
    if (output.slice(edit.start, edit.end) === edit.replacement) continue;
    output = output.slice(0, edit.start) + edit.replacement + output.slice(edit.end);
  }
  // add the import after the last import statement
  let identifier = "t";
  if (/(?:^|\n)\s*(?:const|let|var|function)\s+t\b/.test(source)) identifier = "translate";
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
  // Self-check: never write a file that no longer parses.
  try {
    parser.parse(output, { sourceType: "module", plugins: ["jsx", "typescript"] });
  } catch (error) {
    broken.push(`${rel}: ${error.message.split("\n")[0]}`);
    continue;
  }
  fs.writeFileSync(file, output);
}

summary.sort((a, b) => b.count - a.count);
console.log(`mode: ${WRITE ? "WRITE" : "dry run"}   files: ${files.length}`);
console.log("wrap sites:", JSON.stringify(stats));
console.log("unique keys:", keys.size);
console.log("\n-- files touched (top 30) --");
for (const row of summary.slice(0, 30)) console.log(`  ${String(row.count).padStart(3)}  ${row.rel}`);
console.log(`\n-- module-level objects needing manual handling (${manual.length}) --`);
for (const row of manual.slice(0, 20)) console.log(`  ${row.rel}:${row.line}  ${JSON.stringify(row.text)}`);
if (broken.length) {
  console.log(`\n!! skipped (would not parse) : ${broken.length}`);
  for (const row of broken) console.log("   ", row);
}
fs.writeFileSync(
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/i18n-manual.json",
  JSON.stringify({ manual, broken }, null, 1),
);

fs.writeFileSync(
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/i18n-keys.json",
  JSON.stringify([...keys].sort(), null, 1),
);
console.log("\nwrote tools/i18n-keys.json");
