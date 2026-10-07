import { cn } from "@heroui/react";
import { LOCALES, type Locale, setLocale, useLocale } from "@/i18n";

/**
 * Language picker. Uses a native <select> so it works identically on the login
 * screen (no HeroUI provider needed) and inside settings.
 */
export function LocaleSelect({ className }: { className?: string }) {
  const locale = useLocale();
  return (
    <select
      aria-label="Language"
      value={locale}
      onChange={(event) => setLocale(event.target.value as Locale)}
      className={cn(
        "h-9 rounded-lg border border-border bg-surface px-2 text-sm text-foreground",
        "hover:bg-default/30 focus:outline-none focus:ring-2 focus:ring-accent/40",
        className,
      )}
    >
      {LOCALES.map((entry) => (
        <option key={entry.id} value={entry.id}>
          {entry.native}
        </option>
      ))}
    </select>
  );
}
