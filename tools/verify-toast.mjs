// Focused check on the error-toast path that used to crash with the insertBefore error.
import fs from "node:fs";
import path from "node:path";

const PW = "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const PROFILE = "C:\\Users\\ptfm\\Downloads\\chrometest\\profile3";
const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(
  path.join(PROFILE, "Default", "Preferences"),
  JSON.stringify({
    translate: { enabled: true },
    translate_allowlists: { en: "zh-CN" },
    account_values: { translate_allowlists: { en: "zh-CN" } },
    intl: { app_locale: "zh-CN" },
  }),
  "utf8",
);

const ctx = await chromium.launchPersistentContext(PROFILE, {
  executablePath: CHROME,
  headless: false,
  args: ["--lang=zh-CN", "--no-first-run", "--no-default-browser-check"],
  locale: "zh-CN",
});
const page = ctx.pages()[0] ?? (await ctx.newPage());
const errors = [];
page.on("pageerror", (e) => errors.push("PAGEERROR: " + e.message));
page.on("console", (m) => {
  if (m.type() === "error") errors.push("CONSOLE: " + m.text());
});

await page.goto("http://127.0.0.1:8080/login", { waitUntil: "load" });
await page.waitForSelector("input", { timeout: 20000 });
await page.waitForTimeout(4000);

// Fail the sign-in request immediately (the real Telegram call can take 30s+),
// so the error-toast render path is exercised quickly instead of waiting it out.
await page.route("**/api/v1/auth/telegram/start", (route) =>
  route.fulfill({
    status: 500,
    contentType: "application/json",
    body: JSON.stringify({ error: { code: "internal_error", message: "request failed" } }),
  }),
);

const input = page.locator("input").first();
await input.click();
await input.type("+9999999999", { delay: 30 });
await page.locator("button", { hasText: /Send code/ }).first().click();

let toastText = "";
const deadline = Date.now() + 30000;
while (Date.now() < deadline) {
  const t = await page.locator("[data-sonner-toast]").allInnerTexts();
  if (t.length) {
    toastText = t.join(" | ").replace(/\s+/g, " ").trim();
    break;
  }
  await page.waitForTimeout(400);
}

console.log("toast seen:", toastText ? JSON.stringify(toastText) : "(none within 30s)");
console.log("insertBefore errors:", errors.filter((e) => e.includes("insertBefore")).length);
console.log("all page errors:", errors.length ? JSON.stringify(errors, null, 2) : "none");
await page.screenshot({ path: "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/verify-toast.png" });
await ctx.close();
