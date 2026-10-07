// Reproduce the login-page "insertBefore" crash with the real browser.
// Usage: node repro-login.mjs
const PW = "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const URL_ = "http://127.0.0.1:8080/login";
const PHONE = "+8613800138000";

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

function attach(page, bucket) {
  page.on("pageerror", (e) => bucket.push("PAGEERROR: " + e.message));
  page.on("console", (m) => {
    if (m.type() === "error") bucket.push("CONSOLE: " + m.text());
  });
}

async function run(label, mutate) {
  const browser = await chromium.launch({ executablePath: CHROME, headless: true });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const errors = [];
  attach(page, errors);
  await page.goto(URL_, { waitUntil: "networkidle" });
  await page.waitForSelector("input", { timeout: 15000 });

  if (mutate) {
    // Emulate what Google Translate does: wrap text nodes in <font> elements,
    // which takes them out of React's expected DOM shape.
    const wrapped = await page.evaluate(() => {
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      const nodes = [];
      while (walker.nextNode()) {
        const t = walker.currentNode;
        if (t.nodeValue && t.nodeValue.trim().length > 1) nodes.push(t);
      }
      for (const t of nodes) {
        const font = document.createElement("font");
        font.setAttribute("class", "translated-ltr");
        t.parentNode.insertBefore(font, t);
        font.appendChild(t);
      }
      return nodes.length;
    });
    console.log(`  [${label}] wrapped ${wrapped} text nodes (translate emulation)`);
  }

  const input = page.locator("input").first();
  await input.click();
  for (const ch of PHONE) {
    await input.press(ch === "+" ? "Shift+Digit3" : ch);
    await page.waitForTimeout(30);
  }
  await page.waitForTimeout(700);
  const value = await input.inputValue();
  const visible = await page.locator("text=Send code").count();
  await page.screenshot({ path: `C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/repro-${label}.png` });
  console.log(`  [${label}] input value="${value}" sendButton=${visible}`);
  console.log(`  [${label}] errors: ${errors.length ? JSON.stringify(errors, null, 2) : "none"}`);
  await browser.close();
  return errors.length;
}

console.log("== A: plain typing ==");
const a = await run("plain", false);
console.log("== B: typing after translate-like DOM mutation ==");
const b = await run("translated", true);
console.log(`\nRESULT plain=${a} errors, translated=${b} errors`);
