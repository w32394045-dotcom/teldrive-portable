// Performance harness for the Teldrive SPA: measures cold first-load transfer
// size and painted timings against a running server with a mocked API, so
// "faster" becomes a number instead of an opinion.
//
//   node tools/perf-audit.mjs before
//   node tools/perf-audit.mjs after
//
// Writes tools/perf-<label>.json and prints a human summary.
import fs from "node:fs";
import path from "node:path";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const label = process.argv[2] || "run";
const OUT = path.join("C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools", `perf-${label}.json`);
const PROFILE = `C:\\Users\\ptfm\\Downloads\\chrometest\\perf-${label}`;

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const now = "2026-07-22T12:00:00Z";
const me = {
  userId: 1,
  displayName: "Fixture User",
  username: "fixture",
  premium: true,
  role: "owner",
  capabilities: [
    "files.read", "files.write", "files.share", "system.manageUsers", "system.manageJobs",
    "system.manageQueues", "system.localImport", "system.maintenance", "system.owner",
  ],
  createdAt: now,
};
const files = Array.from({ length: 40 }, (_, index) => ({
  id: `${String(index).padStart(2, "0")}111111-1111-4111-8111-111111111111`,
  name: index % 4 === 0 ? `Folder ${index}` : `document-number-${index}.pdf`,
  kind: index % 4 === 0 ? "folder" : "file",
  mimeType: index % 4 === 0 ? undefined : "application/pdf",
  size: 1024 * (index + 1),
  encryption: true,
  status: "active",
  modTime: now,
  generation: 1,
  createdAt: now,
  updatedAt: now,
}));

const routes = [
  { name: "files", path: "/files", ready: "text=document-number-1.pdf" },
  { name: "storage", path: "/storage", ready: "text=/Storage|存储|ストレージ|스토리지|儲存/" },
  { name: "settings-appearance", path: "/settings/appearance", ready: "text=/Appearance|外观|外觀|外観|모양/" },
  { name: "login", path: "/login", ready: "select" },
];

fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(
  path.join(PROFILE, "Default", "Preferences"),
  JSON.stringify({ intl: { app_locale: "zh-CN" } }),
  "utf8",
);

const browser = await chromium.launchPersistentContext(PROFILE, {
  executablePath: CHROME,
  headless: false,
  args: ["--lang=zh-CN", "--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
  locale: "zh-CN",
  viewport: { width: 1440, height: 900 },
});

const report = { label, base: BASE, measuredAt: new Date().toISOString(), routes: [] };

for (const route of routes) {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error.message)));
  await page.route("**/api/v1/**", (request) => {
    const url = new URL(request.request().url());
    const p = url.pathname.replace(/^\/api/, "");
    const json = (body) =>
      request.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/v1/me") return json(me);
    if (p === "/v1/files") return json({ items: files });
    if (p === "/v1/files/statistics/drive")
      return json({ totalFiles: 30, totalFolders: 10, totalBytes: 840, trashedFiles: 0, activeShares: 0, openUploads: 0 });
    if (p === "/v1/jobs/statistics") return json({ running: 0, available: 0, completed: 0, failed: 0 });
    if (p === "/v1/me/photo") return request.fulfill({ status: 204, body: "" });
    return json({});
  });
  await page.addInitScript((value) => window.localStorage.setItem("teldrive.locale", value), "zh-CN");
  await page.context().clearCookies();

  const started = Date.now();
  await page.goto(`${BASE}${route.path}`, { waitUntil: "load" });
  const loadMs = Date.now() - started;
  let readyMs = null;
  try {
    await page.locator(route.ready).first().waitFor({ state: "visible", timeout: 20000 });
    readyMs = Date.now() - started;
  } catch {
    readyMs = null;
  }
  const metrics = await page.evaluate(() => {
    const navigation = performance.getEntriesByType("navigation")[0];
    const resources = performance.getEntriesByType("resource").map((entry) => ({
      name: new URL(entry.name).pathname,
      initiatorType: entry.initiatorType,
      // transferSize is what actually crossed the wire (0 when served from cache).
      transferSize: entry.transferSize,
      decodedBodySize: entry.decodedBodySize,
      encodedBodySize: entry.encodedBodySize,
      duration: Math.round(entry.duration),
    }));
    return {
      domContentLoaded: Math.round(navigation.domContentLoadedEventEnd),
      loadEvent: Math.round(navigation.loadEventEnd),
      responseStart: Math.round(navigation.responseStart),
      transferSize: navigation.transferSize,
      resources,
    };
  });

  const transferred = metrics.resources.reduce((sum, r) => sum + r.transferSize, 0);
  const decoded = metrics.resources.reduce((sum, r) => sum + r.decodedBodySize, 0);
  const js = metrics.resources.filter((r) => /\.(js|mjs)$/.test(r.name));
  const css = metrics.resources.filter((r) => r.name.endsWith(".css"));
  const entry = {
    name: route.name,
    path: route.path,
    loadMs,
    readyMs,
    domContentLoaded: metrics.domContentLoaded,
    responseStart: metrics.responseStart,
    requests: metrics.resources.length,
    transferredBytes: transferred,
    decodedBytes: decoded,
    jsRequests: js.length,
    jsTransferred: js.reduce((sum, r) => sum + r.transferSize, 0),
    jsDecoded: js.reduce((sum, r) => sum + r.decodedBodySize, 0),
    cssTransferred: css.reduce((sum, r) => sum + r.transferSize, 0),
    cssDecoded: css.reduce((sum, r) => sum + r.decodedBodySize, 0),
    largest: [...metrics.resources]
      .sort((a, b) => b.transferSize - a.transferSize)
      .slice(0, 8)
      .map((r) => `${r.name} ${(r.transferSize / 1024).toFixed(0)}KB/${(r.decodedBodySize / 1024).toFixed(0)}KB`),
    errors,
  };
  report.routes.push(entry);
  console.log(
    `[${route.name}] load ${loadMs}ms  ready ${readyMs ?? "n/a"}ms  requests ${entry.requests}  ` +
      `wire ${(transferred / 1024).toFixed(0)}KB  decoded ${(decoded / 1024).toFixed(0)}KB  ` +
      `JS ${(entry.jsTransferred / 1024).toFixed(0)}KB->${(entry.jsDecoded / 1024).toFixed(0)}KB  ` +
      `CSS ${(entry.cssTransferred / 1024).toFixed(0)}KB->${entry.cssDecoded / 1024 / 1024 > 0.4 ? (entry.cssDecoded / 1024).toFixed(0) : (entry.cssDecoded / 1024).toFixed(0)}KB`,
  );
  console.log(`    top: ${entry.largest.join(" | ")}`);
  await page.close();
}

await browser.close();
fs.writeFileSync(OUT, JSON.stringify(report, null, 2), "utf8");
console.log(`\nwrote ${OUT}`);
