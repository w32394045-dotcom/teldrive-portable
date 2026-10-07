// Minimal HTTPS fetcher using Node's TLS stack (schannel/curl is broken on this host).
// Usage: node nfetch.mjs <url> [outFile] [--progress]
import { createWriteStream, mkdirSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import https from "node:https";
import http from "node:http";

function get(url, redirects = 0) {
  return new Promise((res, rej) => {
    if (redirects > 8) return rej(new Error("too many redirects"));
    const mod = url.startsWith("http:") ? http : https;
    const req = mod.get(
      url,
      {
        headers: {
          "user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) nfetch/1.0",
          accept: "*/*",
        },
        timeout: 120000,
      },
      (r) => {
        if ([301, 302, 303, 307, 308].includes(r.statusCode) && r.headers.location) {
          r.resume();
          const next = new URL(r.headers.location, url).toString();
          res(get(next, redirects + 1));
          return;
        }
        res(r);
      },
    );
    req.on("error", rej);
    req.on("timeout", () => req.destroy(new Error("timeout")));
  });
}

const args = process.argv.slice(2);
const url = args[0];
const out = args.find((a) => !a.startsWith("--") && a !== url);
const progress = args.includes("--progress");
if (!url) {
  console.error("usage: node nfetch.mjs <url> [outFile] [--progress]");
  process.exit(2);
}

const r = await get(url);
if (r.statusCode !== 200) {
  let body = "";
  for await (const c of r) body += c;
  console.error(`HTTP ${r.statusCode} for ${url}\n${body.slice(0, 500)}`);
  process.exit(1);
}
if (!out) {
  let body = "";
  for await (const c of r) body += c;
  process.stdout.write(body);
} else {
  const target = resolve(out);
  mkdirSync(dirname(target), { recursive: true });
  const ws = createWriteStream(target);
  let done = 0;
  const total = Number(r.headers["content-length"] || 0);
  let last = 0;
  for await (const chunk of r) {
    ws.write(chunk);
    done += chunk.length;
    if (progress && Date.now() - last > 3000) {
      last = Date.now();
      const pct = total ? ((done / total) * 100).toFixed(1) + "%" : "";
      console.error(`  ${(done / 1048576).toFixed(1)}MB / ${(total / 1048576).toFixed(1)}MB ${pct}`);
    }
  }
  await new Promise((ok) => ws.end(ok));
  console.error(`saved ${target} (${statSync(target).size} bytes)`);
}
