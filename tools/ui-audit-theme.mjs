// Light/dark theme audit for the Teldrive v2 UI.
//
// For every route x viewport x theme it:
//   1. asserts the *effective* theme (html class, data-theme, computed
//      color-scheme, resolved theme tokens, body/html background) and whether
//      the requested theme actually took effect,
//   2. computes a WCAG 2.1 contrast ratio for every visible text-bearing
//      element, resolving the effective background by alpha-compositing the
//      ancestor background-color chain (Porter-Duff "over"),
//   3. flags wrong-side backgrounds (dark box on a light page / vice versa),
//   4. writes screenshots to tools/ui-light/ and tools/ui-dark/,
//   5. runs an extra "light-forced" pass (data-theme removed at runtime, DOM
//      only, no source change) to show what the intended light palette looks
//      like — this separates "one hardcoded attribute" from "bad palette".
//
// Colour handling: this Chrome returns oklch() verbatim from
// getComputedStyle (it does NOT normalise to rgb()), and canvas fillStyle
// preserves the colour space too, so the probe converts oklch -> sRGB with
// explicit OKLab maths and cross-checks that maths against the canvas
// rasteriser on every page (see converterCheck in the JSON).
//
// READ-ONLY: nothing under src/ is written.
import fs from "node:fs";
import path from "node:path";

const PW =
  "file:///C:/Users/ptfm/Documents/deepseek-harness/default-workspace/src/teldrive-2/ui/node_modules/playwright-core/index.js";
const CHROME = "C:\\Program Files\\Google\\Chrome Beta\\Application\\chrome.exe";
const BASE = process.env.APP_URL || "http://127.0.0.1:8080";
const TOOLS = "C:/Users/ptfm/Documents/deepseek-harness/default-workspace/tools";
const SHOTS = {
  light: path.join(TOOLS, "ui-light"),
  dark: path.join(TOOLS, "ui-dark"),
  forced: path.join(TOOLS, "ui-light-forced"),
};
const PROFILE = "C:\\Users\\ptfm\\Downloads\\chrometest\\ui-theme";

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
// Same fixtures as tools/ui-audit.mjs (proven against the real OpenAPI shapes).
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
const sessions = [{ id: uuid(21), client: "Chrome on Windows", ip: "127.0.0.1", createdAt: now, lastUsedAt: now, current: true }];
const users = [
  { userId: 1, displayName: "Fixture User", username: "fixture", role: "owner", premium: true, createdAt: now },
  { userId: 2, displayName: "一个非常长的中文用户名用于测试布局边界", username: "a-very-long-username-here", role: "user", premium: false, createdAt: now },
];
const apiKeys = [{ id: uuid(31), name: "automation-key-with-a-long-name", createdAt: now, lastUsedAt: now }];
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
const forcedLightRoutes = ["files", "login", "storage", "settings-appearance", "tasks"];

const viewports = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "mobile", width: 390, height: 844 },
];
const LOCALE = process.env.LOCALE || "zh-CN";

const apiRoute = (route) => {
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
};

// ── In-page probe ─────────────────────────────────────────────────────────
const probe = () => {
  // ── colour parsing: rgb/hex/color(srgb)/oklch/oklab (this Chrome keeps them) ──
  const oklabToSRGB = (L, a, b, alpha) => {
    const l_ = L + 0.3963377774 * a + 0.2158037573 * b;
    const m_ = L - 0.1055613458 * a - 0.0638541728 * b;
    const s_ = L - 0.0894841775 * a - 1.291485548 * b;
    const l = l_ ** 3;
    const m = m_ ** 3;
    const s = s_ ** 3;
    const lr = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s;
    const lg = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s;
    const lb = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s;
    const enc = (v) => {
      const c = v <= 0.0031308 ? 12.92 * v : 1.055 * v ** (1 / 2.4) - 0.055;
      return Math.min(255, Math.max(0, c * 255));
    };
    return { r: enc(lr), g: enc(lg), b: enc(lb), a: alpha };
  };
  const fromOklch = (L, C, H, alpha) => {
    const hr = (H * Math.PI) / 180;
    return oklabToSRGB(L, C * Math.cos(hr), C * Math.sin(hr), alpha);
  };
  const num = (token, percentScale) => {
    if (token === undefined) return null;
    if (token.endsWith("%")) return (Number.parseFloat(token) / 100) * (percentScale ?? 1);
    const v = Number.parseFloat(token);
    return Number.isFinite(v) ? v : null;
  };
  const parseColor = (value) => {
    if (!value || value === "transparent" || value === "none") return null;
    const s = String(value).trim();
    const rgbM = /^rgba?\(([^)]+)\)$/i.exec(s);
    if (rgbM) {
      const parts = rgbM[1].split(/[,\s/]+/).filter(Boolean);
      if (parts.length < 3) return null;
      const ch = parts.slice(0, 3).map((t) => num(t, 2.55));
      if (ch.some((v) => v === null)) return null;
      const a = parts.length > 3 ? (num(parts[3], 1) ?? 1) : 1;
      return { r: ch[0], g: ch[1], b: ch[2], a };
    }
    const hexM = /^#([0-9a-f]{3,8})$/i.exec(s);
    if (hexM) {
      let h = hexM[1];
      if (h.length === 3 || h.length === 4) h = h.split("").map((c) => c + c).join("");
      if (h.length !== 6 && h.length !== 8) return null;
      return {
        r: Number.parseInt(h.slice(0, 2), 16),
        g: Number.parseInt(h.slice(2, 4), 16),
        b: Number.parseInt(h.slice(4, 6), 16),
        a: h.length === 8 ? Number.parseInt(h.slice(6, 8), 16) / 255 : 1,
      };
    }
    const okM = /^oklch\(\s*([^)]+)\)$/i.exec(s);
    if (okM) {
      const parts = okM[1].split("/");
      const head = parts[0].trim().split(/\s+/);
      const alpha = parts.length > 1 ? (num(parts[1].trim(), 1) ?? 1) : 1;
      if (head.length < 3) return null;
      let L = num(head[0], 1);
      const C = num(head[1], 0.4) ?? 0;
      const H = head[2] === "none" ? 0 : Number.parseFloat(head[2]);
      if (L === null) return null;
      if (L > 1) L /= 100; // tolerate a raw "13" without the % sign
      return fromOklch(L, C, Number.isFinite(H) ? H : 0, alpha);
    }
    const oklabM = /^oklab\(\s*([^)]+)\)$/i.exec(s);
    if (oklabM) {
      const parts = oklabM[1].split("/");
      const head = parts[0].trim().split(/\s+/);
      const alpha = parts.length > 1 ? (num(parts[1].trim(), 1) ?? 1) : 1;
      if (head.length < 3) return null;
      let L = num(head[0], 1);
      if (L === null) return null;
      if (L > 1) L /= 100;
      return oklabToSRGB(L, num(head[1], 0.4) ?? 0, num(head[2], 0.4) ?? 0, alpha);
    }
    const srgbM = /^color\(srgb\s+([^)]+)\)$/i.exec(s);
    if (srgbM) {
      const parts = srgbM[1].split(/[\s/]+/).filter(Boolean);
      if (parts.length < 3) return null;
      const ch = parts.slice(0, 3).map((t) => Number.parseFloat(t) * 255);
      const a = parts.length > 3 ? Number.parseFloat(parts[3]) : 1;
      if (ch.some((v) => !Number.isFinite(v))) return null;
      return { r: ch[0], g: ch[1], b: ch[2], a: Number.isFinite(a) ? a : 1 };
    }
    return null; // lab()/lch()/hwb(): reported as unmeasurable, never guessed
  };

  const channel = (v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const luminance = (c) => 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
  const contrast = (a, b) => {
    const l1 = luminance(a);
    const l2 = luminance(b);
    return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
  };
  const over = (src, dst) => {
    const a = src.a + dst.a * (1 - src.a);
    if (a <= 0) return { r: 0, g: 0, b: 0, a: 0 };
    return {
      r: (src.r * src.a + dst.r * dst.a * (1 - src.a)) / a,
      g: (src.g * src.a + dst.g * dst.a * (1 - src.a)) / a,
      b: (src.b * src.a + dst.b * dst.a * (1 - src.a)) / a,
      a,
    };
  };
  const css = (c) =>
    !c ? "none" : c.a >= 0.999
      ? `rgb(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)})`
      : `rgba(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)}, ${Number(c.a.toFixed(3))})`;

  // Self-test: OKLab maths vs the browser's own rasteriser (canvas getImageData).
  const converterCheck = (() => {
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    const samples = [
      "oklch(0 0 0)", "oklch(1 0 0)", "oklch(0.5 0 0)",
      "oklch(0.62796 0.25768 29.234)", // reference: sRGB #ff0000
      "oklch(0.13 0.005 70)", "oklch(0.955 0 0)", "oklch(0.72 0.008 70)",
      "oklch(0.86 0.09 70)", "oklch(0.21 0.008 70 / 0.6)", "rgb(18, 52, 86)",
      // oklab() forms actually emitted by the HeroUI token layer:
      "oklab(0.786273 -0.136523 0.0820315)", "oklab(0.885909 0.0223868 0.0615072)",
      "oklab(0.3585 0.0643477 0.0539942)",
    ];
    const rows = [];
    let maxDelta = 0;
    for (const s of samples) {
      const mine = parseColor(s);
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = s;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      const browser = { r: d[0], g: d[1], b: d[2], a: d[3] / 255 };
      const delta = Math.max(
        Math.abs(mine.r - browser.r), Math.abs(mine.g - browser.g),
        Math.abs(mine.b - browser.b), Math.abs(mine.a - browser.a) * 255,
      );
      maxDelta = Math.max(maxDelta, delta);
      rows.push({ value: s, math: css(mine), browserRaster: css(browser), delta: Number(delta.toFixed(2)) });
    }
    return { samples: rows, maxDelta: Number(maxDelta.toFixed(2)), agrees: maxDelta <= 2.5 };
  })();

  const root = document.documentElement;
  const bodyStyle = getComputedStyle(document.body);
  const rootStyle = getComputedStyle(root);
  const pageBgRaw = parseColor(bodyStyle.backgroundColor) || parseColor(rootStyle.backgroundColor);
  const TOKEN_NAMES = ["--background", "--foreground", "--surface", "--surface-foreground", "--muted", "--muted-background",
    "--overlay", "--default", "--default-foreground", "--accent", "--accent-foreground", "--sidebar",
    "--sidebar-foreground", "--border", "--field-background", "--danger"];
  const tokens = {};
  for (const name of TOKEN_NAMES) {
    const raw = rootStyle.getPropertyValue(name).trim();
    tokens[name] = raw;
  }
  const parsedBg = pageBgRaw ? { r: pageBgRaw.r, g: pageBgRaw.g, b: pageBgRaw.b } : null;
  const page = {
    htmlClass: root.className,
    dataTheme: root.getAttribute("data-theme"),
    hasDarkClass: root.classList.contains("dark"),
    hasLightClass: root.classList.contains("light"),
    computedColorScheme: rootStyle.colorScheme,
    inlineColorScheme: root.style.colorScheme || null,
    bodyBg: bodyStyle.backgroundColor,
    htmlBg: rootStyle.backgroundColor,
    bodyColor: bodyStyle.color,
    bodyBgResolved: css(pageBgRaw),
    storedTheme: (() => { try { return localStorage.getItem("theme"); } catch { return "n/a"; } })(),
    prefersDark: window.matchMedia("(prefers-color-scheme: dark)").matches,
    tokens,
    tokensHash: JSON.stringify(tokens),
    bodyBgLuminance: parsedBg ? Number(luminance(parsedBg).toFixed(4)) : null,
  };
  const pageIsDark = pageBgRaw ? luminance(pageBgRaw) < 0.2 : null;

  const describe = (el) => {
    const id = el.id ? `#${el.id}` : "";
    const cls = (el.getAttribute("class") || "").toString().split(/\s+/).filter(Boolean).slice(0, 3).map((c) => `.${c}`).join("");
    return `${el.tagName.toLowerCase()}${id}${cls}`;
  };
  const ownText = (el) =>
    Array.from(el.childNodes).filter((n) => n.nodeType === 3)
      .map((n) => n.textContent.replace(/\s+/g, " ").trim()).join(" ").trim();
  const hiddenByAncestor = (el) => {
    for (let n = el; n && n !== root.parentElement; n = n.parentElement) {
      if (n.getAttribute && n.getAttribute("aria-hidden") === "true") return true;
    }
    return false;
  };
  const disabledish = (el) => {
    for (let n = el; n && n !== root.parentElement; n = n.parentElement) {
      if (n.disabled === true || n.getAttribute?.("aria-disabled") === "true" || n.hasAttribute?.("data-disabled")) return true;
    }
    return false;
  };

  const violations = [];
  const overImages = [];
  const transparentText = [];
  const disabledLowContrast = [];
  const unparsed = [];
  const leftovers = [];
  const skipped = { srOnly: 0, noText: 0, invisible: 0, ariaHidden: 0 };
  let measured = 0;
  let lowestRatio = null;

  for (const el of Array.from(document.querySelectorAll("body *"))) {
    if (el.classList.contains("sr-only")) { skipped.srOnly++; continue; }
    const text = ownText(el);
    if (!text) { skipped.noText++; continue; }
    if (hiddenByAncestor(el)) { skipped.ariaHidden++; continue; }
    const cs = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1 || cs.display === "none" || cs.visibility === "hidden" || cs.contentVisibility === "hidden") { skipped.invisible++; continue; }
    if (Number(cs.opacity) <= 0.02) { skipped.invisible++; continue; }

    const fill = cs.webkitTextFillColor && cs.webkitTextFillColor !== cs.color ? cs.webkitTextFillColor : null;
    const rawFg = fill || cs.color;
    let fg = parseColor(rawFg);
    if (!fg) { unparsed.push({ selector: describe(el), text: text.slice(0, 50), color: rawFg }); continue; }

    const layers = [];
    let gradient = false;
    let image = false;
    for (let n = el; n; n = n.parentElement) {
      const ncs = getComputedStyle(n);
      const bi = ncs.backgroundImage;
      if (bi && bi !== "none") {
        if (/gradient\(/i.test(bi)) gradient = true;
        if (/url\(/i.test(bi)) image = true;
      }
      const c = parseColor(ncs.backgroundColor);
      if (c && c.a > 0) layers.push(c);
    }
    let bg = { r: 255, g: 255, b: 255, a: 0 };
    for (let i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
    const incomplete = bg.a < 0.999;
    if (incomplete) bg = over(bg, { r: 255, g: 255, b: 255, a: 1 });

    let opacity = 1;
    for (let n = el; n && n !== root.parentElement; n = n.parentElement) {
      opacity *= Number(getComputedStyle(n).opacity);
      if (opacity <= 0.001) break;
    }
    const faded = opacity < 0.999;
    if (faded) fg = over({ ...fg, a: fg.a * opacity }, bg);

    const fontSize = Number.parseFloat(cs.fontSize);
    const weight = Number.parseInt(cs.fontWeight, 10) || 400;
    const bold = weight >= 700;
    const large = fontSize >= 24 || (bold && fontSize >= 18.66);
    const required = large ? 3 : 4.5;

    if (fg.a < 0.05) {
      transparentText.push({ selector: describe(el), text: text.slice(0, 70), color: cs.color, bg: css(bg), fontSize, bold });
      continue;
    }

    const r = contrast(fg, bg);
    measured++;
    if (lowestRatio === null || r < lowestRatio.ratio) {
      lowestRatio = { ratio: Number(r.toFixed(2)), selector: describe(el), text: text.slice(0, 50) };
    }
    const entry = {
      selector: describe(el),
      text: text.slice(0, 70),
      color: css(fg),
      rawColor: rawFg,
      bg: css(bg),
      ratio: Number(r.toFixed(2)),
      required,
      fontSize: Number(fontSize.toFixed(1)),
      fontWeight: weight,
      large,
      disabled: disabledish(el),
      faded: faded ? Number(opacity.toFixed(3)) : null,
      incompleteBg: incomplete || null,
    };
    if (gradient || image) { overImages.push(entry); continue; }
    if (r < required) {
      if (entry.disabled) disabledLowContrast.push(entry);
      else violations.push(entry);
    }
  }

  // Wrong-side backgrounds: an element painting its own opaque-ish background
  // whose lightness contradicts the page backdrop. A dark *accent* surface on a
  // light page is legitimate design, so accent-coloured surfaces are counted
  // separately instead of being reported as unthemed leftovers.
  const accent = parseColor(rootStyle.getPropertyValue("--accent").trim());
  const near = (a, b, tol = 6) => a && b && Math.abs(a.r - b.r) <= tol && Math.abs(a.g - b.g) <= tol && Math.abs(a.b - b.b) <= tol;
  let accentSurfaces = 0;
  for (const el of Array.from(document.querySelectorAll("body *"))) {
    const cs = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    if (rect.width < 24 || rect.height < 16) continue;
    if (cs.display === "none" || cs.visibility === "hidden" || Number(cs.opacity) <= 0.05) continue;
    if (["IMG", "CANVAS", "VIDEO", "SVG", "IFRAME"].includes(el.tagName)) continue;
    const own = parseColor(cs.backgroundColor);
    if (!own || own.a < 0.5) continue;
    if (near(own, accent)) { accentSurfaces++; continue; }
    const l = luminance(own);
    const wrongSide = pageIsDark === true ? l > 0.7 : pageIsDark === false ? l < 0.15 : false;
    if (!wrongSide) continue;
    leftovers.push({
      selector: describe(el),
      background: css(own),
      luminance: Number(l.toFixed(4)),
      pageIsDark,
      text: (el.innerText || "").replace(/\s+/g, " ").trim().slice(0, 50),
    });
  }

  const hardcodedPanels = {};
  for (const cls of ["glass-panel", "glass-panel-lg", "warm-gradient"]) {
    const nodes = Array.from(document.querySelectorAll(`.${cls}`)).filter((n) => n.getBoundingClientRect().width > 0);
    if (!nodes.length) continue;
    hardcodedPanels[cls] = nodes.slice(0, 4).map((n) => ({
      selector: describe(n),
      bg: getComputedStyle(n).backgroundColor,
      bgImage: getComputedStyle(n).backgroundImage.slice(0, 100),
    }));
  }

  return {
    page, pageIsDark, measured, violations, overImages, transparentText,
    disabledLowContrast, unparsed, leftovers, accentSurfaces, hardcodedPanels, skipped, lowestRatio, converterCheck,
  };
};

const results = [];
fs.mkdirSync(path.join(PROFILE, "Default"), { recursive: true });
fs.writeFileSync(path.join(PROFILE, "Default", "Preferences"), JSON.stringify({ intl: { app_locale: LOCALE } }), "utf8");
for (const dir of Object.values(SHOTS)) fs.mkdirSync(dir, { recursive: true });

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: false,
  args: ["--no-first-run", "--no-default-browser-check", "--disable-features=Translate"],
});

// theme runs: light and dark are driven by localStorage before boot.
const runs = [
  { theme: "light", storage: "light", forceLight: false, routes: routes.map((r) => r.name) },
  { theme: "dark", storage: "dark", forceLight: false, routes: routes.map((r) => r.name) },
  // Diagnostic: the DOM attribute is removed after load so the *intended*
  // light palette applies (no source change; this isolates the attribute bug).
  { theme: "light-forced", storage: "light", forceLight: true, routes: forcedLightRoutes },
];

// TOGGLE_ONLY=1 skips the route sweep and only re-runs the switcher test,
// merging the result into the existing JSON (cheap re-verification).
const TOGGLE_ONLY = process.env.TOGGLE_ONLY === "1";

for (const run of TOGGLE_ONLY ? [] : runs) {
  for (const viewport of viewports) {
    if (run.forceLight && viewport.name !== "desktop") continue;
    const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height }, locale: LOCALE });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e.message).slice(0, 200)));
    page.on("console", (m) => { if (m.type() === "error") errors.push(`console: ${m.text().slice(0, 200)}`); });
    await page.route("**/api/v1/**", apiRoute);
    await page.addInitScript((value) => { try { window.localStorage.setItem("theme", value); } catch {} }, run.storage);

    for (const routeName of run.routes) {
      const route = routes.find((r) => r.name === routeName);
      errors.length = 0;
      const key = `${route.name}@${viewport.name}`;
      const shotDir = run.theme === "light" ? SHOTS.light : run.theme === "dark" ? SHOTS.dark : SHOTS.forced;
      const shot = path.join(shotDir, `${key.replace(/[^a-zA-Z0-9]+/g, "_")}.png`);
      let result;
      try {
        await page.goto(`${BASE}${route.path}`, { waitUntil: "load" });
        await page.waitForTimeout(1400);
        if (run.forceLight) {
          // DOM-only simulation of the fix: drop the stale attribute.
          await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
          await page.waitForTimeout(350);
        }
        result = await page.evaluate(probe);
        await page.screenshot({ path: shot });
      } catch (error) {
        results.push({ theme: run.theme, route: route.name, viewport: viewport.name, error: String(error.message).slice(0, 300) });
        console.log(`  ERR ${run.theme} ${key}: ${error.message.slice(0, 110)}`);
        continue;
      }
      results.push({
        theme: run.theme, route: route.name, viewport: viewport.name, screenshot: shot,
        errors: [...errors], forced: run.forceLight, ...result,
      });
      const p = result.page;
      const themeOk = run.theme === "dark"
        ? p.hasDarkClass && p.dataTheme === "dark"
        : !p.hasDarkClass && !p.dataTheme; // a real light page has neither
      console.log(
        `  ${run.theme.padEnd(12)} ${key.padEnd(30)} class=${String(p.htmlClass).padEnd(7)} ` +
          `data-theme=${String(p.dataTheme).padEnd(6)} scheme=${String(p.computedColorScheme).padEnd(12)} ` +
          `bodyBg=${String(p.bodyBgResolved).padEnd(20)} themeOk=${themeOk ? "Y" : "N"} viol=${result.violations.length}/${result.measured}`,
      );
    }
    await context.close();
    console.log(`done ${run.theme}/${viewport.name}`);
  }
}

// ── Theme switcher: applies without reload, then persists ────────────────
const toggle = { steps: [] };
{
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: LOCALE });
  const page = await context.newPage();
  await page.route("**/api/v1/**", apiRoute);
  const read = () =>
    page.evaluate(() => {
      const root = document.documentElement;
      return {
        htmlClass: root.className,
        dataTheme: root.getAttribute("data-theme"),
        computedColorScheme: getComputedStyle(root).colorScheme,
        inlineColorScheme: root.style.colorScheme || null,
        bodyBg: getComputedStyle(document.body).backgroundColor,
        bodyColor: getComputedStyle(document.body).color,
        storedTheme: (() => { try { return localStorage.getItem("theme"); } catch { return "n/a"; } })(),
      };
    });
  // Deliberately NO addInitScript here: a page-level init script re-seeds
  // localStorage on every navigation and would mask whether the user's choice
  // really persisted across a reload. Seed once, then reload, then observe.
  await page.goto(`${BASE}/settings/appearance`, { waitUntil: "load" });
  await page.waitForTimeout(900);
  await page.evaluate(() => { try { window.localStorage.setItem("theme", "dark"); } catch {} });
  await page.reload({ waitUntil: "load" });
  await page.waitForTimeout(1300);
  toggle.steps.push({ step: "initial(dark, seeded once then reloaded)", ...(await read()) });
  await page.screenshot({ path: path.join(SHOTS.dark, "toggle_1_initial_dark.png") });
  let clicked = null;
  try {
    const button = page.getByRole("button", { name: /浅色|Light|亮色|ライト|라이트/ }).first();
    await button.click({ timeout: 6000 });
    clicked = (await button.innerText().catch(() => "")).replace(/\s+/g, " ").trim();
  } catch (error) {
    toggle.steps.push({ step: "click-light-failed", error: String(error.message).slice(0, 200) });
  }
  await page.waitForTimeout(1000);
  toggle.clickedLabel = clicked;
  toggle.steps.push({ step: "after-click-light(no reload)", ...(await read()) });
  await page.screenshot({ path: path.join(SHOTS.light, "toggle_2_after_click_light.png") });

  await page.reload({ waitUntil: "load" });
  await page.waitForTimeout(1300);
  toggle.steps.push({ step: "after-reload", ...(await read()) });
  await page.screenshot({ path: path.join(SHOTS.light, "toggle_3_after_reload.png") });

  try {
    const darkBtn = page.getByRole("button", { name: /深色|Dark|ダーク|다크/ }).first();
    await darkBtn.click({ timeout: 6000 });
    await page.waitForTimeout(900);
    toggle.steps.push({ step: "after-click-dark-again", ...(await read()) });
  } catch (error) {
    toggle.steps.push({ step: "click-dark-failed", error: String(error.message).slice(0, 200) });
  }
  await context.close();
}

await browser.close();

const payload = {
  base: BASE,
  locale: LOCALE,
  measuredAt: new Date().toISOString(),
  methodology: {
    colourPipeline:
      "Chrome returns oklch() verbatim from getComputedStyle in this build, so colours are converted with explicit OKLab->linear-sRGB->sRGB maths. converterCheck per page compares that maths against the browser's own canvas rasteriser (getImageData).",
    background:
      "Effective background = Porter-Duff 'over' composite of the element's own background-color and every ancestor background-color (nearest on top). If no layer was fully opaque the chain is composited over white and the entry carries incompleteBg:true.",
    opacity: "Element + ancestor opacity is multiplied and the text colour is faded into the backdrop before measuring.",
    excluded:
      "sr-only, aria-hidden, zero-size/hidden, and disabled controls (counted separately) are excluded from violations; text over gradients/images is unmeasurable and reported apart; lab()/lch()/hwb() colours are reported as unparsed rather than guessed.",
    thresholds: "WCAG 2.1 AA: 4.5:1 normal text, 3:1 large text (>=24px, or bold >=18.66px).",
  },
  results,
  toggle,
};
const jsonPath = path.join(TOOLS, "ui-light-report.json");
if (TOGGLE_ONLY) {
  // Preserve the sweep results; only refresh the switcher evidence.
  const previous = JSON.parse(fs.readFileSync(jsonPath, "utf8"));
  previous.toggle = toggle;
  previous.toggleMeasuredAt = new Date().toISOString();
  fs.writeFileSync(jsonPath, JSON.stringify(previous, null, 2), "utf8");
  console.log("=== theme switcher (toggle-only re-run) ===");
  for (const step of toggle.steps) console.log(`  ${step.step}: ${JSON.stringify(step)}`);
  console.log(`\nupdated toggle section in ${jsonPath}`);
} else {
  fs.writeFileSync(jsonPath, JSON.stringify(payload, null, 2), "utf8");
}

// ── Console summary ──────────────────────────────────────────────────────
const themes = [...new Set(results.map((r) => r.theme))];
const totals = {};
for (const theme of themes) {
  const rows = results.filter((r) => r.theme === theme && !r.error);
  const viol = rows.reduce((n, r) => n + r.violations.length, 0);
  const measured = rows.reduce((n, r) => n + r.measured, 0);
  const worst = rows.reduce((m, r) => Math.min(m, ...r.violations.map((v) => v.ratio), Infinity), Infinity);
  totals[theme] = {
    pages: rows.length, violations: viol, measured, worstRatio: Number.isFinite(worst) ? worst : null,
    overGradientOrImage: rows.reduce((n, r) => n + r.overImages.length, 0),
    wrongSideBackgrounds: rows.reduce((n, r) => n + r.leftovers.length, 0),
    disabledLowContrast: rows.reduce((n, r) => n + r.disabledLowContrast.length, 0),
    transparentText: rows.reduce((n, r) => n + r.transparentText.length, 0),
    unparsed: rows.reduce((n, r) => n + r.unparsed.length, 0),
  };
}
console.log("\n=== totals ===");
for (const [theme, t] of Object.entries(totals)) console.log(`  ${theme}: ${JSON.stringify(t)}`);

const firstPerTheme = {};
for (const row of results) {
  if (!row.error && !firstPerTheme[row.theme]) firstPerTheme[row.theme] = row;
}
console.log("\n=== effective theme (first page of each run) ===");
for (const [theme, row] of Object.entries(firstPerTheme)) {
  console.log(`  ${theme}: class="${row.page.htmlClass}" data-theme="${row.page.dataTheme}" scheme=${row.page.computedColorScheme} bodyBg=${row.page.bodyBgResolved} bodyColor=${cssish(row.page.bodyColor)}`);
  console.log(`     tokens: --background=${row.page.tokens["--background"]} --foreground=${row.page.tokens["--foreground"]} --muted=${row.page.tokens["--muted"]}`);
  if (row.converterCheck) console.log(`     converterCheck: agrees=${row.converterCheck.agrees} maxDelta=${row.converterCheck.maxDelta}`);
}
function cssish(v) { return String(v); }

// Do light and dark actually resolve different tokens?
const lightRow = firstPerTheme.light;
const darkRow = firstPerTheme.dark;
if (lightRow && darkRow) {
  console.log(`\n  light tokensHash === dark tokensHash ? ${lightRow.page.tokensHash === darkRow.page.tokensHash}`);
}

console.log("\n=== theme switcher ===");
for (const step of toggle.steps) console.log(`  ${step.step}: ${JSON.stringify(step)}`);

console.log("\n=== worst contrast violations per theme (deduped by selector+ratio) ===");
for (const theme of themes) {
  const rows = results.filter((r) => r.theme === theme && !r.error);
  const seen = new Map();
  for (const row of rows) {
    for (const v of row.violations) {
      const k = `${v.selector}|${v.color}|${v.bg}|${v.ratio}|${v.required}`;
      if (!seen.has(k)) seen.set(k, { ...v, routes: new Set() });
      seen.get(k).routes.add(`${row.route}/${row.viewport}`);
    }
  }
  const list = [...seen.values()].sort((a, b) => a.ratio - b.ratio).slice(0, 15);
  console.log(`\n  --- ${theme} (${seen.size} distinct) ---`);
  for (const v of list) {
    console.log(`   ${String(v.ratio).padStart(6)}:1 (need ${v.required}) ${v.color} on ${v.bg} ${v.fontSize}px/${v.fontWeight} ${v.selector} "${v.text.slice(0, 34)}" [${[...v.routes].slice(0, 3).join(", ")}]`);
  }
}
console.log(`\njson: ${jsonPath}`);
console.log(`shots: ${SHOTS.light} | ${SHOTS.dark} | ${SHOTS.forced}`);
