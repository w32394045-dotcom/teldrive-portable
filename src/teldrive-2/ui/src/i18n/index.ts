import { useSyncExternalStore } from "react";
import ja from "./messages/ja.json";
import ko from "./messages/ko.json";
import zhCN from "./messages/zh-CN.json";
import zhTW from "./messages/zh-TW.json";

/**
 * Minimal runtime i18n.
 *
 * English source text doubles as the message key, so nothing has to be
 * hand-numbered and a missing translation degrades to readable English instead
 * of showing a raw key:
 *
 *   t("Send code")                       -> "发送验证码"
 *   t("Expires {{time}}", { time })      -> "有效期至 12:30"
 *
 * `t` is intentionally a plain module function (not a hook) so it can be
 * called anywhere — including module-level tables — without threading a hook
 * through every component.
 */
export type Locale = "en" | "zh-CN" | "zh-TW" | "ja" | "ko";

export const LOCALES: ReadonlyArray<{ id: Locale; native: string; english: string }> = [
  { id: "en", native: "English", english: "English" },
  { id: "zh-CN", native: "简体中文", english: "Simplified Chinese" },
  { id: "zh-TW", native: "繁體中文", english: "Traditional Chinese" },
  { id: "ja", native: "日本語", english: "Japanese" },
  { id: "ko", native: "한국어", english: "Korean" },
];

const STORAGE_KEY = "teldrive.locale";

type Catalog = Record<string, string>;

const catalogs: Partial<Record<Locale, Catalog>> = {
  "zh-CN": zhCN as Catalog,
  "zh-TW": zhTW as Catalog,
  ja: ja as Catalog,
  ko: ko as Catalog,
};

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && LOCALES.some((entry) => entry.id === value);
}

/** Map any BCP-47 tag onto a supported locale. */
export function normalizeTag(tag: string): Locale {
  const lower = tag.toLowerCase();
  if (lower.startsWith("zh")) {
    return /hant|-tw|-hk|-mo/.test(lower) ? "zh-TW" : "zh-CN";
  }
  if (lower.startsWith("ja")) return "ja";
  if (lower.startsWith("ko")) return "ko";
  return "en";
}

function detectLocale(): Locale {
  if (typeof window === "undefined") return "en";
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (isLocale(stored)) return stored;
  } catch {
    // localStorage can be unavailable in hardened browser modes.
  }
  const tags = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const tag of tags) {
    const locale = normalizeTag(tag ?? "");
    if (locale !== "en") return locale;
  }
  return "en";
}

let current: Locale = detectLocale();
const listeners = new Set<() => void>();

function applyDocumentLang(locale: Locale) {
  if (typeof document === "undefined") return;
  // Keeping <html lang> truthful also stops Chrome from offering to translate
  // the page, which would otherwise rewrite React-managed DOM nodes.
  document.documentElement.lang = locale;
}

applyDocumentLang(current);

export function getLocale(): Locale {
  return current;
}

export function setLocale(next: Locale) {
  if (!isLocale(next) || next === current) return;
  current = next;
  try {
    window.localStorage.setItem(STORAGE_KEY, next);
  } catch {
    // Persisting the choice is best-effort.
  }
  applyDocumentLang(next);
  for (const listener of listeners) listener();
}

export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

const PLACEHOLDER = /\{\{\s*([\w.$-]+)\s*\}\}/g;

/** Translate a message; unknown keys fall back to the English source text. */
export function t(key: string, vars?: Record<string, string | number>): string {
  const catalog = catalogs[current];
  let message = (catalog && catalog[key]) || key;
  if (!vars) return message;
  message = message.replace(PLACEHOLDER, (match, name: string) =>
    vars[name] === undefined ? match : String(vars[name]),
  );
  return message;
}

/** Subscribe a component to locale changes. */
export function useLocale(): Locale {
  return useSyncExternalStore(subscribeLocale, getLocale, () => "en" as Locale);
}

export function useTranslation() {
  const locale = useLocale();
  return { t, locale, setLocale };
}

/** Coverage report used by the i18n verification script. */
export function catalogStats() {
  const source = catalogs[current] ?? {};
  return { locale: current, messages: Object.keys(source).length };
}
