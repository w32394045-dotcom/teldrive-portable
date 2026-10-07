// Reproduce the crash with Chrome's REAL page translation enabled
// (the user's profile has translate_allowlists={"en":"zh-CN"} = always translate English).
import fs from "node:fs";
import path from "node:path";

const PW = "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const PROFILE = "C:\\Users\\ptfm\\Downloads\\chrometest\\profile";
const URL_ = process.env.APP_URL || "http://127.0.0.1:8080/login";
const PHONE = "13800138000";
const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const prefs = {
  translate: { enabled: true },
  translate_allowlists: { en: "zh-CN" },
  account_values: { translate_allowlists: { en: "zh-CN" }, translate_recent_target: "zh-CN" },
  intl: { app_locale: "zh-CN" },
};
fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(path.join(PROFILE, "Default", "Preferences"), JSON.stringify(prefs), "utf8");

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

await page.goto(URL_, { waitUntil: "load" });
await page.waitForSelector("input", { timeout: 20000 });
await page.waitForTimeout(9000); // let Chrome's translator do its DOM rewriting

const dom = await page.evaluate(() => ({
  htmlClass: document.documentElement.className,
  translated: document.documentElement.className.includes("translated"),
  fontTags: document.querySelectorAll("font").length,
  bodyText: document.body.innerText.slice(0, 160).replace(/\n/g, " | "),
}));
console.log("DOM after translate window:", JSON.stringify(dom, null, 2));

// now interact the way the user did
const input = page.locator("input").first();
await input.click();
await input.type(PHONE, { delay: 120 });
await page.waitForTimeout(1500);
console.log("after typing:", JSON.stringify({ value: await input.inputValue(), errors }, null, 2));

// and press Enter / click Send code, which changes the form structure
try {
  const btn = page.locator("button", { hasText: /Send code|发送/ }).first();
  await btn.click({ timeout: 5000 });
  await page.waitForTimeout(3000);
} catch (e) {
  console.log("send-code click failed:", e.message);
}
console.log("after send-code:", JSON.stringify({ errors }, null, 2));
await page.screenshot({ path: "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/repro-translate.png" });
console.log("TOTAL ERRORS:", errors.length);
await ctx.close();
