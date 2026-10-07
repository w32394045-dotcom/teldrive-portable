import { Button } from "@heroui/react";
import { createFileRoute } from "@tanstack/react-router";
import { useTheme } from "next-themes";
import MoonIcon from "~icons/gravity-ui/moon";
import SunIcon from "~icons/gravity-ui/sun";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { t } from "@/i18n";
import { LocaleSelect } from "@/i18n/LocaleSelect";

export const Route = createFileRoute("/_settings/settings/appearance")({
  component: AppearanceSettings,
});

function AppearanceSettings() {
  const { resolvedTheme, setTheme } = useTheme();
  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("Appearance")}
        description={t("Control how Teldrive looks in this browser.")}
      />
      <SettingsSection
        title={t("Color theme")}
        description={t("The choice is stored locally and applies immediately.")}
      >
        <SettingsRow label={t("Theme")} description={t("Choose the light or dark Teldrive visual system.")}>
          <div className="grid grid-cols-2 gap-2">
            <Button
              variant={resolvedTheme === "light" ? "primary" : "secondary"}
              onPress={() => setTheme("light")}
            >
              <SunIcon className="size-4" />
              {t("Light")}
            </Button>
            <Button
              variant={resolvedTheme === "dark" ? "primary" : "secondary"}
              onPress={() => setTheme("dark")}
            >
              <MoonIcon className="size-4" />
              {t("Dark")}
            </Button>
          </div>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection
        title={t("Language")}
        description={t("Pick the interface language. The choice is stored in this browser.")}
      >
        <SettingsRow
          label={t("Language")}
          description={t("Simplified Chinese, Traditional Chinese, Japanese and Korean are available.")}
        >
          <LocaleSelect />
        </SettingsRow>
      </SettingsSection>
    </div>
  );
}
