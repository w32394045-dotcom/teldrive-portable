// Splits a built UI dist into "eager" (what a cold first paint must fetch,
// per index.html) and "deferred" (route/viewer chunks fetched on demand), and
// reports both raw and precompressed sizes. Used to judge code-splitting and
// compression changes with numbers instead of chunk-name intuition.
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";

const dist = process.argv[2] ||
  "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/dist";
const html = fs.readFileSync(path.join(dist, "index.html"), "utf8");

const eager = new Set();
for (const match of html.matchAll(/(?:src|href)="\/?([^"]+)"/g)) {
  const name = match[1];
  if (name.startsWith("assets/")) eager.add(name);
}
const scriptSources = [...html.matchAll(/<script[^>]+src="\/?([^"]+)"/g)].map((m) => m[1]);

const walk = (dir) => {
  const found = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) found.push(...walk(full));
    else found.push(path.relative(dist, full).split(path.sep).join("/"));
  }
  return found;
};

const all = walk(dist).filter((name) => /\.(js|mjs|css)$/.test(name));
const size = (name) => (fs.existsSync(path.join(dist, name)) ? fs.statSync(path.join(dist, name)).size : 0);
// A file counted as eager only if it is referenced by index.html.
const eagerFiles = all.filter((name) => eager.has(name));
const deferredFiles = all.filter((name) => !eager.has(name));

const summarize = (files) => {
  const raw = files.reduce((total, name) => total + size(name), 0);
  const brotli = files.reduce((total, name) => {
    const sibling = path.join(dist, `${name}.br`);
    return total + (fs.existsSync(sibling) ? fs.statSync(sibling).size : size(name));
  }, 0);
  const gzip = files.reduce((total, name) => {
    const buffer = fs.existsSync(path.join(dist, name)) ? fs.readFileSync(path.join(dist, name)) : Buffer.alloc(0);
    return total + (buffer.length ? zlib.gzipSync(buffer, { level: 9 }).length : 0);
  }, 0);
  return { count: files.length, raw, gzip, brotli };
};

const kb = (bytes) => (bytes / 1024).toFixed(1).padStart(9);
for (const [name, files] of [
  ["eager (cold first paint)", eagerFiles],
  ["deferred (on navigation)", deferredFiles],
]) {
  const stats = summarize(files);
  console.log(
    `${name.padEnd(26)} files ${String(stats.count).padStart(3)}   raw ${kb(stats.raw)} KB   gzip ${kb(stats.gzip)} KB   brotli ${kb(stats.brotli)} KB`,
  );
}

console.log(`\nentry script(s): ${scriptSources.join(", ")}`);
console.log("\neager files by brotli size:");
for (const name of eagerFiles.sort((a, b) => size(b) - size(a)).slice(0, 12)) {
  const sibling = `${name}.br`;
  const compressed = fs.existsSync(path.join(dist, sibling)) ? fs.statSync(path.join(dist, sibling)).size : 0;
  console.log(`  ${name.padEnd(48)} raw ${kb(size(name))}  br ${kb(compressed)}`);
}
