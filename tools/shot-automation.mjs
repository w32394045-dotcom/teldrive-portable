// Captures the automation part of the WebDAV settings page (autostart + mount),
// which sits below the fold of the full-page screenshot.
import fs from "node:fs";
import path from "node:path";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const OUT = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/display-i18n";

const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const now = "2026-07-22T12:00:00Z";
const me = {
  userId: 1, displayName: "Fixture User", username: "fixture", premium: true, role: "owner",
  capabilities: ["files.read", "files.write", "system.manageUsers", "system.owner", "system.maintenance"],
  createdAt: now,
};

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: false,
  args: ["--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
});

for (const [locale, name] of [["zh-CN", "自动化"], ["en", "automation"]]) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale });
  const page = await context.newPage();
  await page.route("**/api/v1/**", (route) => {
    const p = new URL(route.request().url()).pathname.replace(/^\/api/, "");
    const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/v1/me") return json(me);
    if (p === "/v1/me/photo") return route.fulfill({ status: 204, body: "" });
    return json({ items: [] });
  });
  await page.route("**/api/webdav-config", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ enabled: true, url: "http://127.0.0.1:8080/webdav" }),
    }),
  );
  await page.route("**/api/system/**", (route) => {
    const p = new URL(route.request().url()).pathname;
    const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p.endsWith("/autostart")) {
      return json({ supported: true, enabled: true, command: '"C:\\teldrive\\teldrive.exe" --autostart' });
    }
    return json({
      supported: true,
      mounted: false,
      drive: "Z:",
      url: "http://127.0.0.1:8080/webdav",
      prerequisites: {
        ready: false,
        needsAdmin: true,
        fixCommand: "Set-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Services\\WebClient' -Name 'BasicAuthLevel' -Value 2 -Type DWord",
        items: [
          { key: "webclient_service", ok: false, current: "手动", required: "自动", description: "server text" },
          { key: "basic_auth_level", ok: false, current: "1", required: "2", description: "server text" },
          { key: "file_size_limit", ok: false, current: "48 MB", required: ">= 1 GB", description: "server text" },
        ],
      },
    });
  });
  await page.addInitScript((value) => {
    window.localStorage.setItem("teldrive.locale", value);
    window.localStorage.setItem("theme", "dark");
  }, locale);

  await page.goto(`${BASE}/settings/webdav`, { waitUntil: "load" });
  await page.waitForTimeout(2500);
  const section = page.getByText("保持常驻").first();
  const target = (await section.count()) > 0 ? section : page.getByText("Keep it running").first();
  await target.scrollIntoViewIfNeeded();
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(OUT, `${locale}-webdav-${name}.png`) });
  console.log(`saved ${locale}`);
  await context.close();
}

await browser.close();
