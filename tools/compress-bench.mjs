// Tunes the compression level: size vs. wall-clock for the entry bundle, so the
// server can compress lazily on first request without stalling the first paint.
import fs from "node:fs";
import zlib from "node:zlib";

const file = process.argv[2] ||
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/dist/assets/index-CwjVmNc9.js";
const buffer = fs.readFileSync(file);
console.log(`input ${(buffer.length / 1024).toFixed(0)} KB\n`);
console.log("codec level      size      time");
const rows = [];
for (const level of [1, 3, 4, 5, 6, 9, 11]) {
  const params = { params: { [zlib.constants.BROTLI_PARAM_QUALITY]: level, [zlib.constants.BROTLI_PARAM_SIZE_HINT]: buffer.length } };
  zlib.brotliCompressSync(buffer, params); // warm up
  const started = process.hrtime.bigint();
  const output = zlib.brotliCompressSync(buffer, params);
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  rows.push(["brotli", level, output.length, ms]);
}
for (const level of [1, 4, 6, 9]) {
  zlib.gzipSync(buffer, { level });
  const started = process.hrtime.bigint();
  const output = zlib.gzipSync(buffer, { level });
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  rows.push(["gzip", level, output.length, ms]);
}
for (const [codec, level, size, ms] of rows) {
  console.log(`${codec} ${String(level).padStart(4)}   ${(size / 1024).toFixed(1).padStart(8)} KB  ${ms.toFixed(0).padStart(6)} ms`);
}
