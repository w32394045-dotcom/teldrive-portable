// i18n acceptance test: render the real app in every locale with a mocked API
// and assert the visible labels really switch language.
import fs from "node:fs";
import path from "node:path";

const PW = "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const PROFILE = "C:\\Users\\ptfm\\Downloads\\chrometest\\i18n";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const MSG_DIR = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/src/i18n/messages";
const SHOTS = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools";

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const catalogs = Object.fromEntries(
  ["en", "zh-CN", "zh-TW", "ja", "ko"].map((locale) => {
    if (locale === "en") return [locale, {}];
    return [locale, JSON.parse(fs.readFileSync(path.join(MSG_DIR, `${locale}.json`), "utf8"))];
  }),
);
const tr = (locale, key) => catalogs[locale][key] ?? key;

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
const files = [
  { id: "11111111-1111-4111-8111-111111111111", name: "Documents", kind: "folder", encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
  { id: "22222222-2222-4222-8222-222222222222", name: "fixture.txt", kind: "file", mimeType: "text/plain", size: 128, encryption: true, status: "active", modTime: now, generation: 1, createdAt: now, updatedAt: now },
];

fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(path.join(PROFILE, "Default", "Preferences"), JSON.stringify({ intl: { app_locale: "zh-CN" } }), "utf8");

const browser = await chromium.launchPersistentContext(PROFILE, {
  executablePath: CHROME,
  headless: false,
  args: ["--lang=zh-CN", "--no-first-run", "--no-default-browser-check"],
  locale: "zh-CN",
  viewport: { width: 1440, height: 900 },
});

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  -> " + detail : ""}`);
};

for (const locale of ["en", "zh-CN", "zh-TW", "ja", "ko"]) {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push("PAGEERROR: " + e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push("CONSOLE: " + m.text());
  });
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url());
    const p = url.pathname.replace(/^\/api/, "");
    const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/v1/me") return json(me);
    if (p === "/v1/files") return json({ items: files });
    if (p === "/v1/files/statistics/drive")
      return json({ totalFiles: 1, totalFolders: 1, totalBytes: 128, trashedFiles: 0, activeShares: 0, openUploads: 0 });
    if (p === "/v1/jobs/statistics") return json({ running: 0, available: 0, completed: 0, failed: 0 });
    if (p === "/v1/me/photo") return route.fulfill({ status: 204, body: "" });
    return json({});
  });
  await page.addInitScript((value) => {
    window.localStorage.setItem("teldrive.locale", value);
  }, locale);

  // 1. file browser (authenticated shell: sidebar + top bar)
  await page.goto(`${BASE}/files`, { waitUntil: "load" });
  await page.waitForTimeout(2500);
  const lang = await page.evaluate(() => document.documentElement.lang);
  const body = await page.evaluate(() => document.body.innerText);
  const expectFiles = tr(locale, "Files");
  const expectTrash = tr(locale, "Trash");
  check(`[${locale}] <html lang> is ${locale}`, lang === locale, `lang=${lang}`);
  check(`[${locale}] sidebar shows "${expectFiles}"`, body.includes(expectFiles));
  check(`[${locale}] sidebar shows "${expectTrash}"`, body.includes(expectTrash));
  await page.screenshot({ path: path.join(SHOTS, `i18n-${locale}-files.png`) });

  // 2. appearance settings (contains the language switcher)
  await page.goto(`${BASE}/settings/appearance`, { waitUntil: "load" });
  await page.waitForTimeout(1800);
  const settingsBody = await page.evaluate(() => document.body.innerText);
  const expectLanguage = tr(locale, "Language");
  check(`[${locale}] settings shows "${expectLanguage}"`, settingsBody.includes(expectLanguage));
  await page.screenshot({ path: path.join(SHOTS, `i18n-${locale}-settings.png`) });

  // 3. login screen
  await page.goto(`${BASE}/login`, { waitUntil: "load" });
  await page.waitForTimeout(1500);
  const loginBody = await page.evaluate(() => document.body.innerText);
  const expectSignIn = tr(locale, "Sign in with Telegram");
  check(`[${locale}] login shows "${expectSignIn}"`, loginBody.includes(expectSignIn));
  check(`[${locale}] language picker present on login`, (await page.locator("select").count()) > 0);
  await page.screenshot({ path: path.join(SHOTS, `i18n-${locale}-login.png`) });

  const realErrors = errors.filter((e) => !e.includes("Failed to load resource"));
  check(`[${locale}] no runtime errors`, realErrors.length === 0, realErrors.join(" | ").slice(0, 160));
  await page.close();
}

await browser.close();
const failed = results.filter((r) => !r.ok);
console.log(`\nSUMMARY: ${results.length - failed.length}/${results.length} checks passed`);
process.exit(failed.length ? 1 : 0);
