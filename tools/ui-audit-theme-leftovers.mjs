// Targeted probe for the two hardcoded-dark leftovers that the route sweep
// never renders (they live in dialogs / transient UI):
//   1. .glass-panel / .glass-panel-lg / .warm-gradient from globals.css, which
//      have NO light-theme override (fixed oklch lightness ~0.21/0.23/0.12).
//   2. the Sonner <Toaster theme="dark"> override in main.tsx, whose toast
//      background is a fixed oklch(0.21 0.008 70 / 0.85).
// Method: render the real class/values on a synthetic node inside the live page
// and read back computed style + WCAG contrast. DOM-only, no source change.
import fs from "node:fs";
const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const mod = await import(PW);
const { chromium } = mod.default && mod.default.chromium ? mod.default : mod;

const measure = () => {
  // Same OKLab maths as tools/ui-audit-theme.mjs.
  const oklabToSRGB = (L, a, b, alpha) => {
    const l_ = L + 0.3963377774 * a + 0.2158037573 * b;
    const m_ = L - 0.1055613458 * a - 0.0638541728 * b;
    const s_ = L - 0.0894841775 * a - 1.291485548 * b;
    const l = l_ ** 3, m = m_ ** 3, s = s_ ** 3;
    const enc = (v) => { const c = v <= 0.0031308 ? 12.92 * v : 1.055 * v ** (1 / 2.4) - 0.055; return Math.min(255, Math.max(0, c * 255)); };
    return {
      r: enc(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
      g: enc(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
      b: enc(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
      a: alpha,
    };
  };
  const parse = (value) => {
    if (!value || value === "transparent") return null;
    const s = String(value).trim();
    let m = /^rgba?\(([^)]+)\)$/i.exec(s);
    if (m) {
      const p = m[1].split(/[,\s/]+/).filter(Boolean);
      const ch = p.slice(0, 3).map((t) => (t.endsWith("%") ? Number.parseFloat(t) * 2.55 : Number.parseFloat(t)));
      return { r: ch[0], g: ch[1], b: ch[2], a: p.length > 3 ? Number.parseFloat(p[3]) : 1 };
    }
    m = /^oklch\(\s*([^)]+)\)$/i.exec(s);
    if (m) {
      const parts = m[1].split("/");
      const h = parts[0].trim().split(/\s+/);
      const alpha = parts.length > 1 ? Number.parseFloat(parts[1]) : 1;
      let L = h[0].endsWith("%") ? Number.parseFloat(h[0]) / 100 : Number.parseFloat(h[0]);
      if (L > 1) L /= 100;
      const C = h[1].endsWith("%") ? Number.parseFloat(h[1]) * 0.004 : Number.parseFloat(h[1]);
      const H = (Number.parseFloat(h[2]) * Math.PI) / 180;
      return oklabToSRGB(L, C * Math.cos(H), C * Math.sin(H), Number.isFinite(alpha) ? alpha : 1);
    }
    m = /^oklab\(\s*([^)]+)\)$/i.exec(s);
    if (m) {
      const h = m[1].split(/[\s/]+/).filter(Boolean);
      let L = h[0].endsWith("%") ? Number.parseFloat(h[0]) / 100 : Number.parseFloat(h[0]);
      if (L > 1) L /= 100;
      return oklabToSRGB(L, Number.parseFloat(h[1]), Number.parseFloat(h[2]), 1);
    }
    return null;
  };
  const lin = (v) => { const c = v / 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
  const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const over = (src, dst) => {
    const a = src.a + dst.a * (1 - src.a);
    return { r: (src.r * src.a + dst.r * dst.a * (1 - src.a)) / a, g: (src.g * src.a + dst.g * dst.a * (1 - src.a)) / a, b: (src.b * src.a + dst.b * dst.a * (1 - src.a)) / a, a };
  };
  const css = (c) => (c.a >= 0.999 ? `rgb(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)})` : `rgba(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)}, ${c.a.toFixed(3)})`);

  const pageBg = parse(getComputedStyle(document.body).backgroundColor);
  const out = { pageBg: css(pageBg), pageBgLuminance: Number(lum(pageBg).toFixed(4)), panels: [], toast: null };

  const host = document.createElement("div");
  host.style.position = "fixed";
  host.style.left = "-9999px";
  host.style.top = "0";
  document.body.appendChild(host);

  // 1. Real CSS classes from globals.css, with real text inside.
  for (const cls of ["glass-panel", "glass-panel-lg", "warm-gradient"]) {
    const box = document.createElement("div");
    box.className = cls;
    // text-foreground is the app's default text colour inside these panels.
    const label = document.createElement("span");
    label.className = "text-foreground";
    label.textContent = "Sample panel text";
    box.appendChild(label);
    host.appendChild(box);
    const boxStyle = getComputedStyle(box);
    const labelStyle = getComputedStyle(label);
    const own = parse(boxStyle.backgroundColor);
    // Composite the panel over the page backdrop.
    const effective = own && own.a < 1 ? over(own, pageBg) : own;
    const fg = parse(labelStyle.color);
    const hasGradient = boxStyle.backgroundImage && boxStyle.backgroundImage !== "none";
    out.panels.push({
      cls,
      backgroundColor: boxStyle.backgroundColor,
      backgroundImage: hasGradient ? boxStyle.backgroundImage.slice(0, 120) : null,
      effectiveOverPage: effective ? css(effective) : "n/a",
      effectiveLuminance: effective ? Number(lum(effective).toFixed(4)) : null,
      textColor: labelStyle.color,
      textContrastOverPanel: effective && fg ? Number(ratio(fg, effective).toFixed(2)) : null,
    });
    box.remove();
  }

  // 2. The Sonner toast override from main.tsx, verbatim.
  const toast = document.createElement("div");
  toast.style.background = "oklch(0.21 0.008 70 / 0.85)";
  toast.style.border = "1px solid oklch(0.95 0.02 70 / 0.1)";
  toast.style.backdropFilter = "blur(16px)";
  toast.style.color = "oklch(0.955 0 0)"; // sonner's dark-theme text
  toast.textContent = "Toast sample";
  host.appendChild(toast);
  const ts = getComputedStyle(toast);
  const tbgOwn = parse(ts.backgroundColor);
  const tbg = over(tbgOwn, pageBg);
  const tfg = parse(ts.color);
  out.toast = {
    backgroundDeclared: "oklch(0.21 0.008 70 / 0.85)",
    backgroundColorComputed: ts.backgroundColor,
    effectiveOverPage: css(tbg),
    effectiveLuminance: Number(lum(tbg).toFixed(4)),
    textColor: ts.color,
    textContrast: Number(ratio(tfg, tbg).toFixed(2)),
  };
  host.remove();
  return out;
};

const browser = await chromium.launch({ executablePath: CHROME, headless: false, args: ["--no-first-run"] });
const report = { measuredAt: new Date().toISOString(), scenarios: {} };
for (const scenario of ["dark", "light-forced"]) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" });
  const page = await context.newPage();
  await page.route("**/api/v1/**", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) }));
  await page.addInitScript(() => { try { window.localStorage.setItem("theme", "light"); } catch {} });
  await page.goto("http://127.0.0.1:8080/files", { waitUntil: "load" });
  await page.waitForTimeout(1400);
  if (scenario === "light-forced") {
    await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
    await page.waitForTimeout(300);
  }
  report.scenarios[scenario] = await page.evaluate(measure);
  await context.close();
}
await browser.close();
const p = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools/ui-theme-leftovers.json";
fs.writeFileSync(p, JSON.stringify(report, null, 2), "utf8");
console.log(JSON.stringify(report, null, 2));
console.log(`\nwritten: ${p}`);
