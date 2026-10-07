// Verifies the upload-settings fix that made every upload fail with a 400.
//
// The settings UI used to offer conflictPolicy="error", which is not in the API
// enum (fail|replace|rename), and the value was persisted and sent verbatim. Two
// things must hold now:
//   1. an already-poisoned localStorage heals on load (persisted value becomes valid)
//   2. choosing "Stop with error" stores the valid enum value "fail"
import fs from "node:fs";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const SETTINGS_KEY = "teldrive.upload-settings.v2";
const VALID = ["fail", "replace", "rename"];

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const now = "2026-07-22T12:00:00Z";
const me = {
  userId: 1, displayName: "Fixture User", username: "fixture", premium: true, role: "owner",
  capabilities: ["files.read", "files.write", "files.share", "system.owner", "system.localImport"],
  createdAt: now,
};

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${ok || !detail ? "" : `  -> ${detail}`}`);
};

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: false,
  args: ["--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
});
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" });

// Seed the exact poisoned value the old UI produced.
await context.addInitScript(
  ({ key, settings }) => {
    window.localStorage.setItem(key, settings);
    window.localStorage.setItem("teldrive.locale", "zh-CN");
    window.localStorage.setItem("theme", "dark");
  },
  {
    key: SETTINGS_KEY,
    settings: JSON.stringify({
      encryption: false,
      conflictPolicy: "error",
      concurrency: 3,
      preferredPartSize: 536870912,
    }),
  },
);

const page = await context.newPage();
const errors = [];
page.on("pageerror", (error) => errors.push(String(error.message).slice(0, 160)));
await page.route("**/api/v1/**", (route) => {
  const url = new URL(route.request().url());
  const p = url.pathname.replace(/^\/api/, "");
  const json = (body) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  if (p === "/v1/me/photo") return route.fulfill({ status: 204, body: "" });
  if (p === "/v1/me") return json(me);
  return json({ items: [] });
});

await page.goto(`${BASE}/settings/uploads`, { waitUntil: "load" });
await page.waitForTimeout(2000);

const poisoned = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), SETTINGS_KEY);
check(
  "poisoned pre-state is present before the fix runs",
  poisoned.conflictPolicy === "error",
  `conflictPolicy=${poisoned.conflictPolicy}`,
);

// Any settings write persists the whole normalised object, so changing the
// concurrency proves what readSettings() decided about the invalid policy.
const increment = page.locator(".number-field__increment-button").first();
if ((await increment.count()) > 0) {
  await increment.click();
  await page.waitForTimeout(600);
} else {
  check("concurrency control found to force a settings write", false, "no increment button");
}

const healed = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), SETTINGS_KEY);
check(
  `invalid "error" policy healed on load (persisted "${healed.conflictPolicy}")`,
  VALID.includes(healed.conflictPolicy),
  `conflictPolicy=${healed.conflictPolicy}`,
);

// Now check the option the user actually picks.
const trigger = page.locator('[role="button"], button').filter({ hasText: /重命名|替换|出错/ }).first();
if ((await trigger.count()) > 0) {
  await trigger.click();
  await page.waitForTimeout(500);
  const option = page.getByText("出错时停止", { exact: true }).last();
  if ((await option.count()) > 0) {
    await option.click();
    await page.waitForTimeout(600);
  } else {
    check('option "出错时停止" present in the picker', false);
  }
} else {
  check("conflict policy select found", false);
}

const chosen = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), SETTINGS_KEY);
check(
  `choosing "Stop with error" stores the valid enum (persisted "${chosen.conflictPolicy}")`,
  VALID.includes(chosen.conflictPolicy),
  `conflictPolicy=${chosen.conflictPolicy}`,
);
check(
  'the stored policy is "fail", not the old "error"',
  chosen.conflictPolicy === "fail",
  `conflictPolicy=${chosen.conflictPolicy}`,
);

const shot = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/verify-upload-settings.png";
await page.screenshot({ path: shot });
check("no runtime errors", errors.length === 0, errors.join(" | "));

await browser.close();
const failed = results.filter((result) => !result.ok);
console.log(`\nSUMMARY: ${results.length - failed.length}/${results.length} checks passed`);
console.log(`screenshot: ${shot}`);
process.exit(failed.length ? 1 : 0);
