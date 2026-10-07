// Reports raw / gzip / brotli sizes for built UI assets so compression choices
// are based on numbers. Uses Node's zlib (brotli included), no dependencies.
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";

const dir = process.argv[2] || "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/dist/assets";
const TEXT = new Set([".js", ".mjs", ".css", ".html", ".json", ".svg", ".txt", ".map"]);

const rows = [];
for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
  if (!entry.isFile()) continue;
  const file = path.join(dir, entry.name);
  const buffer = fs.readFileSync(file);
  if (!TEXT.has(path.extname(entry.name))) continue;
  rows.push({
    name: entry.name,
    raw: buffer.length,
    gzip: zlib.gzipSync(buffer, { level: 9 }).length,
    brotli: zlib.brotliCompressSync(buffer, {
      params: {
        [zlib.constants.BROTLI_PARAM_QUALITY]: 11,
        [zlib.constants.BROTLI_PARAM_SIZE_HINT]: buffer.length,
      },
    }).length,
  });
}
rows.sort((a, b) => b.raw - a.raw);

const kb = (n) => (n / 1024).toFixed(1).padStart(8);
console.log("asset".padEnd(34) + "raw".padStart(11) + "gzip".padStart(11) + "brotli".padStart(11));
for (const row of rows.slice(0, 14)) {
  console.log(row.name.padEnd(34) + kb(row.raw) + kb(row.gzip) + kb(row.brotli));
}
const sum = (key) => rows.reduce((total, row) => total + row[key], 0);
console.log(
  "TOTAL text assets".padEnd(34) + kb(sum("raw")) + kb(sum("gzip")) + kb(sum("brotli")) +
    `   gzip ${((sum("gzip") / sum("raw")) * 100).toFixed(0)}%  brotli ${((sum("brotli") / sum("raw")) * 100).toFixed(0)}%`,
);

// The critical path: what a cold first paint must download.
const initial = rows.filter((row) => /^(index-.*\.(js|css)|react-dom-.*\.js)$/.test(row.name));
if (initial.length) {
  console.log(
    "\ncold first paint".padEnd(34) + kb(initial.reduce((t, r) => t + r.raw, 0)) +
      kb(initial.reduce((t, r) => t + r.gzip, 0)) + kb(initial.reduce((t, r) => t + r.brotli, 0)),
  );
}
