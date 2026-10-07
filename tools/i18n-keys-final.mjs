// Ground truth: every key actually passed to t()/translate() in the UI source.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const UI = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui";
const WS = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace";
const require = createRequire(UI + "/package.json");
const parser = require("@babel/parser");
const traverse = require("@babel/traverse").default;

const SKIP_FILES = [/routeTree\.gen\.ts$/, /[\\/]api[\\/]schema\.ts$/, /[\\/]gen[\\/]/];

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
const keys = new Map();
const placeholders = new Map();

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
  traverse(ast, {
    CallExpression(p) {
      const callee = p.node.callee;
      const name =
        callee.type === "Identifier"
          ? callee.name
          : callee.type === "MemberExpression" && callee.property.type === "Identifier"
            ? callee.property.name
            : null;
      if (name !== "t" && name !== "translate") return;
      const first = p.node.arguments[0];
      if (!first) return;
      let key = null;
      if (first.type === "StringLiteral") key = first.value;
      else if (first.type === "TemplateLiteral" && first.expressions.length === 0) {
        key = first.quasis.map((q) => q.value.cooked ?? "").join("");
      } else {
        console.log(`WARN ${rel}: dynamic t() argument (not a static key)`);
        return;
      }
      if (!keys.has(key)) keys.set(key, new Set());
      keys.get(key).add(rel);
      const holders = [...String(key).matchAll(/\{\{\s*([\w.$-]+)\s*\}\}/g)].map((m) => m[1]);
      if (holders.length) placeholders.set(key, holders);
    },
  });
}

const sorted = [...keys.keys()].sort();
console.log("static t() keys found:", sorted.length);
console.log("files scanned:", files.length);
console.log("keys with placeholders:", placeholders.size);
const outKeys = path.join(WS, "tools", "i18n-keys-final.json");
fs.writeFileSync(outKeys, JSON.stringify(sorted, null, 1));
const outPh = path.join(WS, "tools", "i18n-placeholders.json");
fs.writeFileSync(outPh, JSON.stringify(Object.fromEntries(placeholders), null, 1));
console.log("wrote", outKeys);
console.log("wrote", outPh);

const msgDir = path.join(UI, "src", "i18n", "messages");
for (const locale of ["zh-CN", "zh-TW", "ja", "ko"]) {
  const data = JSON.parse(fs.readFileSync(path.join(msgDir, `${locale}.json`), "utf8"));
  const have = new Set(Object.keys(data));
  const missing = sorted.filter((k) => !have.has(k));
  console.log(`${locale}: translated=${have.size} missing=${missing.length}`);
  if (missing.length) fs.writeFileSync(path.join(WS, `tools/i18n-batch2-${locale}.json`), JSON.stringify(missing, null, 1));
}
