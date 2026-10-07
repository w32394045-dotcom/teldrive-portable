import { Label, ListBox, NumberField, Select, Switch } from "@heroui/react";
import { createFileRoute } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { MAX_PART_SIZE_MIB, normalizePartSizeMiB, useUploadStore } from "@/features/uploads/store";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/uploads")({ component: UploadSettings });

function UploadSettings() {
  const settings = useUploadStore((state) => state.settings);
  const setSettings = useUploadStore((state) => state.setSettings);

  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("Uploads")}
        description={t("Browser upload concurrency, encryption, conflict handling, and multipart sizing.")}
      />
      <SettingsSection
        title={t("Upload behavior")}
        description={t("These preferences are stored in this browser and apply to new uploads.")}
      >
        <SettingsRow
          label={t("Encryption")}
          description={t("Encrypt file parts with Teldrive's server-managed key before storage.")}
        >
          <Switch
            aria-label={t("Encrypt uploaded files")}
            isSelected={settings.encryption}
            onChange={(isSelected) => setSettings({ encryption: isSelected })}
          >
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Label>{t("Encrypt uploaded files")}</Label>
            </Switch.Content>
          </Switch>
        </SettingsRow>
        <SettingsRow
          label={t("Name conflicts")}
          description={t("Choose what happens when the destination already contains the same name.")}
        >
          <Select
            aria-label={t("Name conflicts")}
            selectedKey={settings.conflictPolicy}
            onSelectionChange={(key) =>
              setSettings({ conflictPolicy: String(key) as typeof settings.conflictPolicy })
            }
          >
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                <ListBox.Item id="rename" textValue={t("Rename new file")}>
                  {t("Rename new file")}
                </ListBox.Item>
                <ListBox.Item id="replace" textValue={t("Replace existing")}>
                  {t("Replace existing")}
                </ListBox.Item>
                {/* The API enum is "fail" | "replace" | "rename"; a stale
                    "error" value here made every upload fail with a 400. */}
                <ListBox.Item id="fail" textValue={t("Stop with error")}>
                  {t("Stop with error")}
                </ListBox.Item>
              </ListBox>
            </Select.Popover>
          </Select>
        </SettingsRow>
        <SettingsRow
          label={t("Concurrent uploads")}
          description={t("Number of browser uploads processed at the same time.")}
        >
          <NumberField
            aria-label={t("Concurrent uploads")}
            value={settings.concurrency}
            minValue={1}
            maxValue={12}
            onChange={(value) =>
              setSettings({ concurrency: Math.max(1, Math.min(12, value ?? 1)) })
            }
          >
            <Label className="sr-only">{t("Concurrent uploads")}</Label>
            <NumberField.Group>
              <NumberField.DecrementButton />
              <NumberField.Input />
              <NumberField.IncrementButton />
            </NumberField.Group>
          </NumberField>
        </SettingsRow>
        <SettingsRow
          label={t("Preferred part size")}
          description={t("Defaults to 512 MiB. Values are rounded to the nearest 16 MiB for encrypted uploads; the server may choose a different size.")}
        >
          <PartSizeField />
        </SettingsRow>
      </SettingsSection>
    </div>
  );
}

function PartSizeField() {
  const preferredPartSize = useUploadStore((state) => state.settings.preferredPartSize);
  const setSettings = useUploadStore((state) => state.setSettings);
  const [value, setValue] = useState(preferredPartSize / 1024 / 1024);
  const valueRef = useRef(value);

  const commit = () => {
    const normalized = normalizePartSizeMiB(valueRef.current);
    valueRef.current = normalized;
    setValue(normalized);
    setSettings({ preferredPartSize: normalized * 1024 * 1024 });
  };

  return (
    <NumberField
      aria-label={t("Preferred part size in MiB")}
      value={value}
      maxValue={MAX_PART_SIZE_MIB}
      onChange={(next) => {
        valueRef.current = next ?? 512;
        setValue(valueRef.current);
      }}
      onBlur={commit}
    >
      <Label className="sr-only">{t("Preferred part size in MiB")}</Label>
      <NumberField.Group>
        <NumberField.DecrementButton />
        <NumberField.Input />
        <span className="pr-2 text-xs text-muted">MiB</span>
        <NumberField.IncrementButton />
      </NumberField.Group>
    </NumberField>
  );
}
