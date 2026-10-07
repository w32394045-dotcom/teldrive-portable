// Verifies the UI-display pass: enum badges, module-level label maps, plural
// messages, and machine identifiers must all render in the selected language.
//
// Assertions are exact-line matches against `document.body.innerText` so that
// checking for the forbidden word "File" cannot be fooled by "Filename".
//
//   node tools/verify-display-i18n.mjs
import fs from "node:fs";
import path from "node:path";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const SHOTS = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/display-i18n";

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
const file = (id, name, kind, extra = {}) => ({
  id: uuid(id), name, kind, encryption: true, status: "active", modTime: now, generation: 1,
  createdAt: now, updatedAt: now, ...extra,
});
const files = [
  file(1, "Documents", "folder"),
  file(2, "report.pdf", "file", { mimeType: "application/pdf", size: 4823449 }),
];
const trashed = [
  file(3, "archive.zip", "file", { status: "trashed", mimeType: "application/zip", size: 998877 }),
  file(4, "Old folder", "folder", { status: "trashed" }),
];
const job = {
  id: uuid(50), status: "completed", type: "teldrive_upload_batch", queue: "default",
  attempt: 1, maxAttempts: 3, priority: 2, tags: [], args: {}, errors: [],
  createdAt: now, updatedAt: now, scheduledAt: now, finalizedAt: now,
};
const periodicJob = {
  id: "teldrive-trash-cleanup", kind: "teldrive_cleanup_trash", args: {}, queue: "maintenance",
  priority: 2, maxAttempts: 10, tags: [], cronExpression: "@every 12h", cronTimezone: "UTC",
  nextRunAt: now, paused: false, createdAt: now,
};
const template = {
  kind: "teldrive_cleanup_trash", label: "Clean up trash",
  description: "Remove expired trash entries.", defaultId: "teldrive-trash-cleanup",
  defaultArgs: {}, defaultQueue: "maintenance", recommendedCron: "@every 12h",
};
const storage = {
  summary: { logicalBytes: 6018641, activeFiles: 4, activeFolders: 2, trashedFiles: 7, trashBytes: 128 },
  growth: [{ day: "2026-07-22", addedBytes: 524288, logicalBytes: 6018641 }],
  categories: [
    { category: "document", totalFiles: 3, totalSize: 4823449 },
    { category: "image", totalFiles: 2, totalSize: 204800 },
  ],
  channels: [{ channelId: 1, name: "main", selected: true, health: "healthy", partCount: 12, storedBytes: 5223449 }],
  cleanup: { trashBytes: 128, staleUploadBytes: 4096, staleUploads: 2, totalReclaimableBytes: 4224 },
  activity: [
    { id: 1, type: "upload.completed", resourceType: "file", resourceId: uuid(2), label: "report.pdf", occurredAt: now },
    { id: 2, type: "file.trashed", resourceType: "file", resourceId: uuid(3), label: "archive.zip", occurredAt: now },
  ],
};
const users = [
  { userId: 1, displayName: "Owner One", username: "owner1", premium: true, role: "owner", disabled: false, createdAt: now, updatedAt: now },
  { userId: 2, displayName: "Admin Two", username: "admin2", premium: false, role: "admin", disabled: false, createdAt: now, updatedAt: now },
];

const table = {
  "/v1/me": me,
  "/v1/files": { items: files },
  "/v1/files/statistics/drive": { totalFiles: 4, totalFolders: 2, totalBytes: 6018641, trashedFiles: 7, activeShares: 0, openUploads: 0 },
  "/v1/files/statistics/trash": { totalFiles: 2, totalFolders: 0, totalBytes: 998877 },
  "/v1/jobs/statistics": { available: 0, cancelled: 0, completed: 1, discarded: 0, pending: 0, retryable: 0, running: 0, scheduled: 0 },
  "/v1/jobs": { tasks: [job], meta: {} },
  "/v1/periodic-jobs": { jobs: [periodicJob] },
  "/v1/periodic-jobs/catalog": { templates: [template] },
  "/v1/storage/stats": storage,
  "/v1/admin/users": users,
  "/v1/sessions": { items: [] },
  "/v1/channels": { items: [{ channelId: 1, channelName: "main", status: "active", createdAt: now, isPrimary: true }] },
  "/v1/api-keys": { items: [] },
  "/v1/bots": { items: [] },
};

// Per locale: route -> { require: [regex or string], forbidLines: [exact strings] }
const EXPECTATIONS = {
  "zh-CN": [
    {
      route: "/trash", name: "trash kind badges",
      require: ["文件夹", "文件"],
      forbidLines: ["File", "Folder"],
      mockFiles: trashed,
    },
    {
      route: "/settings/periodic-jobs", name: "periodic job kind identifier",
      require: ["清理回收站"],
      forbidLines: ["teldrive_cleanup_trash", "Teldrive Cleanup Trash"],
    },
    {
      route: "/settings/users", name: "user role badge",
      require: ["所有者", "管理员"],
      forbidLines: ["owner", "admin", "Owner", "Admin"],
    },
    {
      route: "/storage", name: "category labels, activity feed, plural stats",
      require: ["文档", "图片", "上传已完成", "文件已移入回收站", /\d[\d,]* 个文件夹/, /\d[\d,]* 个文件/, /已选择 \d[\d,]* 项/],
      forbidLines: ["Documents", "Images", "Upload completed", "File moved to Trash"],
    },
  ],
  ja: [
    {
      route: "/trash", name: "trash kind badges",
      require: ["フォルダー", "ファイル"],
      forbidLines: ["File", "Folder"],
      mockFiles: trashed,
    },
    {
      route: "/storage", name: "category labels and plural stats",
      require: ["ドキュメント", "画像", "アップロード完了", /\d[\d,]* 件のフォルダー/, /\d[\d,]* 件のファイル/],
      forbidLines: ["Documents", "Images", "Upload completed"],
    },
  ],
  ko: [
    {
      route: "/storage", name: "category labels and plural stats",
      require: ["문서", "이미지", "업로드 완료", /폴더 \d[\d,]*개/],
      forbidLines: ["Documents", "Images", "Upload completed"],
    },
  ],
};

fs.mkdirSync(SHOTS, { recursive: true });
const browser = await chromium.launch({
  executablePath: CHROME,
  headless: false,
  args: ["--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
});

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${ok || !detail ? "" : `  -> ${detail}`}`);
};

for (const [locale, specs] of Object.entries(EXPECTATIONS)) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale });
  for (const spec of specs) {
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error.message).slice(0, 160)));
    await page.route("**/api/v1/**", (route) => {
      const url = new URL(route.request().url());
      const p = url.pathname.replace(/^\/api/, "");
      const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
      if (p === "/v1/me/photo") return route.fulfill({ status: 204, body: "" });
      if (spec.mockFiles && p === "/v1/files") return json({ items: spec.mockFiles });
      return json(table[p] ?? { items: [] });
    });
    await page.addInitScript((value) => {
      window.localStorage.setItem("teldrive.locale", value);
      window.localStorage.setItem("theme", "dark");
    }, locale);
    await page.goto(`${BASE}${spec.route}`, { waitUntil: "load" });
    await page.waitForTimeout(2200);

    const text = await page.evaluate(() => document.body.innerText);
    const lines = text.split("\n").map((line) => line.trim()).filter(Boolean);
    await page.screenshot({ path: path.join(SHOTS, `${locale}${spec.route.replace(/\//g, "_")}.png`) });

    for (const expected of spec.require) {
      const label = `[${locale}] ${spec.name}: shows ${expected}`;
      if (expected instanceof RegExp) {
        check(label, expected.test(text), `no match in ${lines.length} lines`);
      } else {
        check(label, lines.includes(expected) || text.includes(expected));
      }
    }
    for (const forbidden of spec.forbidLines) {
      const label = `[${locale}] ${spec.name}: no untranslated "${forbidden}"`;
      check(label, !lines.includes(forbidden), `found exact line`);
    }
    check(`[${locale}] ${spec.name}: no runtime errors`, errors.length === 0, errors.join(" | "));
    await page.close();
  }
  await context.close();
}

await browser.close();
const failed = results.filter((result) => !result.ok);
console.log(`\nSUMMARY: ${results.length - failed.length}/${results.length} checks passed`);
if (failed.length) {
  console.log("\nfailures:");
  for (const failure of failed) console.log(`  ${failure.name}  ${failure.detail}`);
}
process.exit(failed.length ? 1 : 0);
