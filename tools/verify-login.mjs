// Acceptance test for the login fixes, run with Chrome's real page translation ENABLED
// (same profile prefs the user has: translate_allowlists = {"en": "zh-CN"}).
import fs from "node:fs";
import path from "node:path";

const PW = "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const PROFILE = "C:\\Users\\ptfm\\Downloads\\chrometest\\profile2";
const URL_ = process.env.APP_URL || "http://127.0.0.1:8080/login";
const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(
  path.join(PROFILE, "Default", "Preferences"),
  JSON.stringify({
    translate: { enabled: true },
    translate_allowlists: { en: "zh-CN" },
    account_values: { translate_allowlists: { en: "zh-CN" }, translate_recent_target: "zh-CN" },
    intl: { app_locale: "zh-CN" },
  }),
  "utf8",
);

const ctx = await chromium.launchPersistentContext(PROFILE, {
  executablePath: CHROME,
  headless: false,
  args: ["--lang=zh-CN", "--no-first-run", "--no-default-browser-check"],
  locale: "zh-CN",
  viewport: { width: 1280, height: 900 },
});
const page = ctx.pages()[0] ?? (await ctx.newPage());
const errors = [];
page.on("pageerror", (e) => errors.push("PAGEERROR: " + e.message));
page.on("console", (m) => {
  if (m.type() === "error") errors.push("CONSOLE: " + m.text());
});

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  -> " + detail : ""}`);
};

await page.goto(URL_, { waitUntil: "load" });
await page.waitForSelector("input", { timeout: 20000 });
await page.waitForTimeout(9000); // give Chrome the same window to auto-translate as before

const dom = await page.evaluate(() => ({
  htmlClass: document.documentElement.className,
  fontTags: document.querySelectorAll("font").length,
  text: document.body.innerText,
}));
check("page is NOT machine-translated", !dom.htmlClass.includes("translated"), `html class="${dom.htmlClass}"`);
check("no <font> nodes injected by translator", dom.fontTags === 0, `font=${dom.fontTags}`);
check("UI text stays as authored (English)", dom.text.includes("Sign in with Telegram"));

// 1) a Chinese user's natural input: no +, no country code
const input = page.locator("input").first();
await input.click();
await input.fill("");
await input.type("13800138000", { delay: 40 });
await page.waitForTimeout(400);
const inlineError = await page.locator("text=Start with + and the country code").count();
const sendBtn = page.locator("button", { hasText: /Send code/ }).first();
const disabledForBadNumber = await sendBtn.isDisabled();
check("missing + shows inline format error", inlineError > 0);
check("Send code disabled for a non-E.164 number", disabledForBadNumber === true);
await page.screenshot({ path: "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/verify-invalid-phone.png" });

// 2) spaced input is normalised automatically
await input.fill("");
await input.type("+86 138 0013 8000", { delay: 40 });
await page.waitForTimeout(400);
const normalized = await input.inputValue();
const disabledForGoodNumber = await sendBtn.isDisabled();
check("spaces/dashes normalised to +8613800138000", normalized === "+8613800138000", `value="${normalized}"`);
check("Send code enabled for a valid E.164 number", disabledForGoodNumber === false);
const formatErrorGone = await page.locator("text=Start with + and the country code").count();
check("inline error cleared once the number is valid", formatErrorGone === 0);

// 3) the exact action that crashed before. Use a number Telegram must reject
// (+999 = unassigned country code) so the error-toast path is exercised without
// sending a login code to any real phone.
await input.fill("");
await input.type("+9999999999", { delay: 30 });
await page.waitForTimeout(300);
const sendBtn2 = page.locator("button", { hasText: /Send code/ }).first();
await page.screenshot({ path: "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/verify-valid-phone.png" });
await sendBtn2.click();
await page.waitForTimeout(6000);
const crash = errors.filter((e) => e.includes("insertBefore"));
check("no insertBefore crash after clicking Send code", crash.length === 0, crash.join(" | "));
const toast = await page.locator("[data-sonner-toast], li[data-sonner-toast]").count();
console.log("   toast elements visible:", toast);
await page.screenshot({ path: "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/verify-after-send.png" });

console.log("\n--- console/page errors ---");
console.log(errors.length ? JSON.stringify(errors, null, 2) : "none");
const failed = results.filter((r) => !r.ok);
console.log(`\nSUMMARY: ${results.length - failed.length}/${results.length} checks passed`);
await ctx.close();
process.exit(failed.length ? 1 : 0);
