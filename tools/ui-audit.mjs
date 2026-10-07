// UI display audit: walks every route in several locales and viewports against
// a mocked API and reports *measurable* display defects (horizontal page
// overflow, clipped/clipped-with-ellipsis text, off-screen elements, undersized
// tap targets, blank pages, console errors) plus screenshots for eyeballing.
//
//   node tools/ui-audit.mjs before
//   node tools/ui-audit.mjs after
import fs from "node:fs";
import path from "node:path";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const label = process.argv[2] || "run";
const TOOLS = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools";
const SHOTS = path.join(TOOLS, `ui-${label}`);
const PROFILE = `C:\\Users\\ptfm\\Downloads\\chrometest\\ui-${label}`;

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const now = "2026-07-22T12:00:00Z";
const uuid = (n) => `${String(n).padStart(8, "0")}-1111-4111-8111-111111111111`;
const me = {
  userId: 1, displayName: "Fixture User", username: "fixture", premium: true, role: "owner",
  capabilities: ["files.read", "files.write", "files.share", "system.manageUsers", "system.manageJobs",
    "system.manageQueues", "system.localImport", "system.maintenance", "system.owner"],
  createdAt: now,
};
// A deliberately awkward library: very long names, many files, mixed kinds.
const files = [
  { id: uuid(1), name: "Documents", kind: "folder", encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: uuid(2), name: "2026-年度财务审计报告-最终修订版本-v3-已签字确认.pdf", kind: "file", mimeType: "application/pdf", size: 4823449, encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: uuid(3), name: "a-really-long-english-file-name-that-should-not-break-the-table-layout-when-translated.tar.gz", kind: "file", mimeType: "application/gzip", size: 128, encryption: false, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: uuid(4), name: "写真集・第1巻.zip", kind: "file", mimeType: "application/zip", size: 998877, encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: uuid(5), name: "휴가-사진-모음-2026-여름-제주도-가족여행.jpg", kind: "file", mimeType: "image/jpeg", size: 204800, encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: uuid(6), name: "影片", kind: "folder", encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
];
const channels = [
  { id: 1, channelId: 1000000000001, channelName: "Teldrive-Storage-Channel-With-A-Very-Long-Name", status: "active", createdAt: now, isPrimary: true },
  { id: 2, channelId: 1000000000002, channelName: "備份頻道", status: "active", createdAt: now, isPrimary: false },
];
const jobs = [
  { id: 1, name: "sync-channel-index", status: "running", createdAt: now, kind: "channel", args: {} },
  { id: 2, name: "cleanup-temp-uploads", status: "completed", createdAt: now, kind: "cleanup", args: {} },
];
const tasks = [
  { id: uuid(11), name: "Uploading a-very-long-file-name-which-might-overflow-the-card.pdf", status: "running", progress: 0.42, createdAt: now, updatedAt: now, size: 4823449, transferred: 2000000 },
  { id: uuid(12), name: "photo.jpg", status: "failed", progress: 0, createdAt: now, updatedAt: now, size: 204800, transferred: 0 },
];
const sessions = [
  { id: uuid(21), client: "Chrome on Windows", ip: "127.0.0.1", createdAt: now, lastUsedAt: now, current: true },
];
const users = [
  { userId: 1, displayName: "Fixture User", username: "fixture", role: "owner", premium: true, createdAt: now },
  { userId: 2, displayName: "一个非常长的中文用户名用于测试布局边界", username: "a-very-long-username-here", role: "user", premium: false, createdAt: now },
];
const apiKeys = [
  { id: uuid(31), name: "automation-key-with-a-long-name", createdAt: now, lastUsedAt: now },
];
// Shapes below follow ui/src/api/schema.ts: several list endpoints return a bare
// array, and the storage dashboard is a nested object, not a collection.
const shared = [{ ...files[1], shareId: uuid(41), permission: "read" }];
const sharedWithMe = [{ file: files[3], permission: "edit", shareId: uuid(42) }];
const storageDashboard = {
  summary: { logicalBytes: 6018641, activeFiles: 4, activeFolders: 2, trashedFiles: 1, trashBytes: 128 },
  growth: [
    { day: "2026-07-20", addedBytes: 1048576, logicalBytes: 3145728 },
    { day: "2026-07-21", addedBytes: 2097152, logicalBytes: 5242880 },
    { day: "2026-07-22", addedBytes: 524288, logicalBytes: 6018641 },
  ],
  categories: [
    { category: "document", totalFiles: 1, totalSize: 4823449 },
    { category: "archive", totalFiles: 2, totalSize: 998877 },
    { category: "image", totalFiles: 1, totalSize: 204800 },
    { category: "video", totalFiles: 0, totalSize: 0 },
    { category: "audio", totalFiles: 0, totalSize: 0 },
    { category: "other", totalFiles: 1, totalSize: 128 },
  ],
  channels: [
    { channelId: 1000000000001, name: "Teldrive-Storage-Channel-With-A-Very-Long-Name", selected: true, health: "healthy", lastCheckedAt: now, partCount: 128, storedBytes: 5223449 },
    { channelId: 1000000000002, name: "備份頻道", selected: false, health: "degraded", partCount: 4, storedBytes: 998877 },
  ],
  cleanup: { trashBytes: 128, staleUploadBytes: 4096, staleUploads: 2, totalReclaimableBytes: 4224 },
  activity: [
    { id: 1, type: "upload", resourceType: "file", resourceId: uuid(2), label: "Uploaded a very long activity label to stress the activity feed layout", occurredAt: now },
    { id: 2, type: "delete", resourceType: "folder", resourceId: uuid(6), label: "Moved to trash", occurredAt: now },
  ],
};
const jobStatistics = { available: 2, cancelled: 1, completed: 5, discarded: 0, pending: 1, retryable: 1, running: 1, scheduled: 0 };

const routes = [
  { name: "files", path: "/files" },
  { name: "shares", path: "/shared" },
  { name: "shared-with-me", path: "/shared-with-me" },
  { name: "storage", path: "/storage" },
  { name: "tasks", path: "/tasks" },
  { name: "trash", path: "/trash" },
  { name: "search", path: "/search" },
  { name: "settings-appearance", path: "/settings/appearance" },
  { name: "settings-uploads", path: "/settings/uploads" },
  { name: "settings-channels", path: "/settings/channels" },
  { name: "settings-api-keys", path: "/settings/api-keys" },
  { name: "settings-bots", path: "/settings/bots" },
  { name: "settings-users", path: "/settings/users" },
  { name: "settings-sessions", path: "/settings/sessions" },
  { name: "settings-periodic-jobs", path: "/settings/periodic-jobs" },
  { name: "login", path: "/login" },
];

const viewports = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "laptop", width: 1280, height: 800 },
  { name: "mobile", width: 390, height: 844 },
];

// Full route sweep on a couple of locales, plus a locale sweep on the routes
// whose labels are longest and whose layout is tightest.
const plan = [
  { locale: "zh-CN", viewport: "desktop", routes: routes.map((r) => r.name) },
  { locale: "en", viewport: "desktop", routes: routes.map((r) => r.name) },
  { locale: "ja", viewport: "desktop", routes: ["files", "settings-appearance", "login", "storage", "settings-users"] },
  { locale: "ko", viewport: "desktop", routes: ["files", "settings-appearance", "login", "storage", "settings-users"] },
  { locale: "zh-TW", viewport: "desktop", routes: ["files", "settings-appearance", "login"] },
  { locale: "zh-CN", viewport: "laptop", routes: routes.map((r) => r.name) },
  { locale: "zh-CN", viewport: "mobile", routes: routes.map((r) => r.name) },
  { locale: "en", viewport: "mobile", routes: ["files", "settings-appearance", "login", "tasks"] },
];

const findings = [];
const record = (severity, entry, detail) => findings.push({ severity, ...entry, ...detail });
const addFinding = (severity, key, message) => findings.push({ severity, key, message });

fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(path.join(PROFILE, "Default", "Preferences"), JSON.stringify({ intl: { app_locale: "zh-CN" } }), "utf8");

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: false,
  args: ["--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
});

const probe = () => {
  const results = { overflowX: null, clipped: [], offscreen: [], smallTargets: [], blank: false };
  const root = document.documentElement;
  results.overflowX = { scrollWidth: root.scrollWidth, innerWidth: window.innerWidth, bodyScrollWidth: document.body.scrollWidth };

  const visible = (element) => {
    const rect = element.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return false;
    const style = getComputedStyle(element);
    return style.visibility !== "hidden" && style.display !== "none" && Number(style.opacity) > 0.05;
  };
  const describe = (element) => {
    const id = element.id ? `#${element.id}` : "";
    const classes = (element.className || "").toString().split(/\s+/).filter(Boolean).slice(0, 3).map((c) => `.${c}`).join("");
    const text = (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 60);
    return `${element.tagName.toLowerCase()}${id}${classes} "${text}"`;
  };
  // An element is inside a scrollable container when any ancestor scrolls.
  const scrollableAncestor = (element) => {
    for (let node = element.parentElement; node && node !== root; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (["auto", "scroll"].includes(style.overflowX) && node.scrollWidth > node.clientWidth + 1) return true;
    }
    return false;
  };

  for (const element of Array.from(document.querySelectorAll("body *"))) {
    if (!visible(element)) continue;
    // Visually-hidden helpers (screen-reader labels) are clipped on purpose.
    if (element.classList.contains("sr-only")) continue;
    const style = getComputedStyle(element);
    const ownText = Array.from(element.childNodes)
      .filter((node) => node.nodeType === Node.TEXT_NODE)
      .map((node) => node.textContent.trim())
      .join(" ")
      .trim();
    const clipsX = ["hidden", "clip"].includes(style.overflowX);
    const clipsY = ["hidden", "clip"].includes(style.overflowY);
    if (ownText && ((clipsX && element.scrollWidth > element.clientWidth + 2) || (clipsY && element.scrollHeight > element.clientHeight + 2))) {
      results.clipped.push({
        selector: describe(element),
        text: ownText.slice(0, 70),
        ellipsis: style.textOverflow === "ellipsis",
        overflow: element.scrollWidth - element.clientWidth,
        tags: `${element.tagName.toLowerCase()}${style.overflowX}/${style.overflowY}`,
      });
    }
    const rect = element.getBoundingClientRect();
    if (rect.width > 0 && (rect.right > window.innerWidth + 1 || rect.left < -1) && !scrollableAncestor(element)) {
      const parentRect = element.parentElement?.getBoundingClientRect();
      results.offscreen.push({
        selector: describe(element),
        left: Math.round(rect.left),
        right: Math.round(rect.right),
        width: Math.round(rect.width),
        parentWidth: parentRect ? Math.round(parentRect.width) : null,
      });
    }
    // Checkbox/radio inputs are visually hidden inside a larger clickable
    // label, so their own box says nothing about the real touch target.
    const hiddenInput = element.tagName === "INPUT" && ["checkbox", "radio"].includes(element.type);
    if (!hiddenInput && ["BUTTON", "A", "INPUT", "SELECT"].includes(element.tagName)) {
      const rect2 = element.getBoundingClientRect();
      if (rect2.width > 0 && rect2.height > 0 && rect2.height < 24) {
        results.smallTargets.push({ selector: describe(element), height: Math.round(rect2.height), width: Math.round(rect2.width) });
      }
    }
  }
  const main = document.querySelector("main") || document.body;
  results.blank = (main.innerText || "").trim().length < 24;
  return results;
};

const keyed = new Map();
for (const step of plan) {
  const viewport = viewports.find((v) => v.name === step.viewport);
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    locale: step.locale,
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error.message).slice(0, 200)));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(`console: ${message.text().slice(0, 200)}`);
  });
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url());
    const p = url.pathname.replace(/^\/api/, "");
    const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    const table = {
      "/v1/me": me,
      "/v1/files": { items: files },
      "/v1/files/statistics/drive": { totalFiles: 4, totalFolders: 2, totalBytes: 6018641, trashedFiles: 1, activeShares: 2, openUploads: 1 },
      "/v1/files/statistics/trash": { totalFiles: 1, totalFolders: 0, totalBytes: 128 },
      "/v1/files/statistics/category": { image: 204800, video: 0, audio: 0, document: 4823449, archive: 998877, other: 128 },
      "/v1/jobs/statistics": jobStatistics,
      "/v1/jobs": { items: jobs },
      "/v1/tasks": { items: tasks },
      "/v1/uploads": { items: tasks },
      "/v1/channels": { items: channels },
      "/v1/channel": channels[0],
      "/v1/sessions": { items: sessions },
      "/v1/users": { items: users },
      "/v1/admin/users": users,
      "/v1/apikeys": { items: apiKeys },
      "/v1/api-keys": { items: apiKeys },
      "/v1/bots": { items: [{ id: 1, name: "teldrive-bot", username: "teldrive_bot", createdAt: now }] },
      "/v1/shares": shared,
      "/v1/shared": shared,
      "/v1/shared/with-me": sharedWithMe,
      "/v1/storage/stats": storageDashboard,
      "/v1/users/search": users,
      "/v1/config": { channelId: channels[0].channelId },
    };
    if (p === "/v1/me/photo") return route.fulfill({ status: 204, body: "" });
    return json(table[p] ?? { items: [] });
  });
  await page.addInitScript((value) => window.localStorage.setItem("teldrive.locale", value), step.locale);

  for (const routeName of step.routes) {
    const route = routes.find((r) => r.name === routeName);
    errors.length = 0;
    await page.goto(`${BASE}${route.path}`, { waitUntil: "load" });
    await page.waitForTimeout(1400);
    let result = null;
    try {
      result = await page.evaluate(probe);
    } catch (error) {
      addFinding("error", `${route.name}@${step.locale}/${step.viewport}`, `probe failed: ${error.message}`);
      continue;
    }
    const key = `${route.name}@${step.locale}/${step.viewport}`;
    const shot = path.join(SHOTS, `${key.replace(/[^a-zA-Z0-9]+/g, "_")}.png`);
    await page.screenshot({ path: shot });

    if (result.overflowX.scrollWidth > result.overflowX.innerWidth + 1) {
      record("error", { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: "page scrolls horizontally", detail: `scrollWidth ${result.overflowX.scrollWidth} > innerWidth ${result.overflowX.innerWidth}` });
    }
    if (result.blank) {
      record("error", { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: "page looks blank", detail: "main content has < 24 characters" });
    }
    // Clipped text without an ellipsis is usually an accidental cut; with an
    // ellipsis it is a deliberate truncation, so it is reported as info.
    for (const item of result.clipped) {
      const severity = item.ellipsis ? "info" : "warn";
      const dedupe = `${key}|${severity}|${item.text}`;
      if (keyed.has(dedupe)) continue;
      keyed.set(dedupe, true);
      record(severity, { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: item.ellipsis ? "text truncated with ellipsis" : "text clipped without ellipsis", detail: `${item.selector} overflow ${item.overflow}px` });
    }
    for (const item of result.offscreen) {
      const dedupe = `${key}|offscreen|${item.selector}`;
      if (keyed.has(dedupe)) continue;
      keyed.set(dedupe, true);
      const reachesIntoPage = item.right > result.overflowX.innerWidth + 1;
      record(reachesIntoPage ? "warn" : "info",
        { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: "element outside the viewport", detail: `${item.selector} left ${item.left} right ${item.right} width ${item.width} (parent ${item.parentWidth})` });
    }
    for (const item of result.smallTargets) {
      const dedupe = `${key}|target|${item.selector}`;
      if (keyed.has(dedupe)) continue;
      keyed.set(dedupe, true);
      record("info", { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: "tap target under 24px tall", detail: `${item.selector} ${item.width}x${item.height}` });
    }
    for (const error of errors) {
      const dedupe = `${key}|error|${error}`;
      if (keyed.has(dedupe)) continue;
      keyed.set(dedupe, true);
      record("error", { route: route.name, locale: step.locale, viewport: step.viewport, screenshot: shot },
        { issue: "runtime error", detail: error });
    }
  }
  await context.close();
  console.log(`done ${step.locale}/${step.viewport} (${step.routes.length} routes)`);
}

await browser.close();
fs.mkdirSync(SHOTS, { recursive: true });
const report = { label, base: BASE, measuredAt: new Date().toISOString(), findings };
fs.writeFileSync(path.join(TOOLS, `ui-audit-${label}.json`), JSON.stringify(report, null, 2), "utf8");

const bySeverity = { error: [], warn: [], info: [] };
for (const finding of findings) bySeverity[finding.severity]?.push(finding);
console.log(`\n=== ${findings.length} findings: ${bySeverity.error.length} error, ${bySeverity.warn.length} warn, ${bySeverity.info.length} info ===`);
for (const severity of ["error", "warn"]) {
  for (const finding of bySeverity[severity]) {
    console.log(`${severity.toUpperCase()}  ${finding.route}@${finding.locale}/${finding.viewport}  ${finding.issue}: ${finding.detail}`);
  }
}
console.log("\n-- truncated text / tap targets by route (info) --");
const grouped = new Map();
for (const finding of bySeverity.info) {
  const group = `${finding.route}@${finding.locale}/${finding.viewport}`;
  grouped.set(group, (grouped.get(group) ?? 0) + 1);
}
for (const [group, count] of [...grouped].slice(0, 40)) console.log(`  ${group}: ${count}`);
console.log(`\nscreenshots: ${SHOTS}`);
console.log(`report: ${path.join(TOOLS, `ui-audit-${label}.json`)}`);
