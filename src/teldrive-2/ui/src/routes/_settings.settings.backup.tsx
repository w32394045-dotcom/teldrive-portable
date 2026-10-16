import {
  Button,
  Chip,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  Switch,
  Typography,
} from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import DownloadIcon from "~icons/gravity-ui/arrow-down-to-line";
import ScanIcon from "~icons/gravity-ui/magnifier";
import { userMessage } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import {
  type BackupSnapshot,
  backupConfigQueryOptions,
  type RestoreMode,
  restoreBackup,
  runBackupNow,
  scanBackups,
  snapshotDownloadUrl,
  updateBackupConfig,
} from "@/features/backup/config";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/backup")({
  component: BackupSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

/** The intervals the page offers. The stored value is shown even when it is not
 * one of these, so a hand-edited interval is visible rather than silently
 * resetting to the first option. */
const INTERVAL_OPTIONS: { key: string; label: string }[] = [
  { key: "1h", label: "Every hour" },
  { key: "6h", label: "Every 6 hours" },
  { key: "12h", label: "Every 12 hours" },
  { key: "24h", label: "Every day" },
  { key: "168h", label: "Every week" },
  { key: "720h", label: "Every 30 days" },
];

const MODE_LABELS: Record<RestoreMode, string> = {
  overwrite: "Overwrite",
  merge: "Merge",
};

function BackupSettings() {
  const config = useQuery(backupConfigQueryOptions());
  const [snapshots, setSnapshots] = useState<BackupSnapshot[] | null>(null);
  const [pendingRestore, setPendingRestore] = useState<BackupSnapshot | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);

  const save = useMutation({
    mutationFn: updateBackupConfig,
    onSuccess: (saved) => {
      // The response is the authoritative state, so a switch can never claim a
      // value the server did not store.
      getQueryClient().setQueryData(queryKeys.backupConfig, saved);
      toast.success(t("Backup settings updated"));
    },
    onError: (error) => {
      toast.error(t("Backup settings could not be saved"), { description: userMessage(error) });
    },
  });

  const run = useMutation({
    mutationFn: runBackupNow,
    onSuccess: () => {
      toast.success(t("A snapshot has been queued and appears here once the worker finishes."));
      // The worker reports back through the settings file rather than the job
      // row, so the last-backup line refreshes on a short delay.
      window.setTimeout(() => {
        void getQueryClient().invalidateQueries({ queryKey: queryKeys.backupConfig });
      }, 4000);
    },
    onError: (error) => {
      toast.error(t("The backup could not be started"), { description: userMessage(error) });
    },
  });

  const scan = useMutation({
    mutationFn: scanBackups,
    onSuccess: (found) => setSnapshots(found),
    onError: (error) => {
      toast.error(t("The channel could not be scanned"), { description: userMessage(error) });
    },
  });

  const restore = useMutation({
    mutationFn: (snapshot: BackupSnapshot) =>
      restoreBackup(snapshot.messageId, config.data?.restoreMode ?? "overwrite"),
    onSuccess: (result) => {
      setPendingRestore(null);
      setAcknowledged(false);
      toast.success(t("The drive was restored."), {
        description: t(
          "Files {{files}} 路 folders {{folders}} 路 parts {{parts}} 路 deleted {{deleted}}",
          {
            files: result.files,
            folders: result.folders,
            parts: result.parts,
            deleted: result.deletedFiles ?? 0,
          },
        ),
      });
      // Every open listing is now describing a catalog that no longer exists.
      void getQueryClient().invalidateQueries({ queryKey: ["files"] });
      void getQueryClient().invalidateQueries({ queryKey: queryKeys.backupConfig });
    },
    onError: (error) => {
      toast.error(t("The snapshot could not be restored"), { description: userMessage(error) });
    },
  });

  if (config.isError) {
    return (
      <>
        <SettingsPageHeader title={t("Backup")} description={t("Snapshots")} />
        <SettingsSection title={t("Backup")}>
          <Typography.Paragraph className="text-sm text-muted">
            {t("Backup could not be loaded")}
          </Typography.Paragraph>
        </SettingsSection>
      </>
    );
  }

  const settings = config.data;
  const enabled = settings?.enabled ?? false;
  const busy = config.isPending || save.isPending;
  const mode: RestoreMode = settings?.restoreMode ?? "overwrite";
  const listed = snapshots ?? lastSnapshot(settings);

  return (
    <>
      <SettingsPageHeader
        title={t("Backup")}
        description={t(
          "Snapshots of the drive index, stored in Telegram. The index is what maps your files to the messages that hold them, so it is the one thing a database loss cannot be recovered from without a backup.",
        )}
      />

      <SettingsSection
        title={t("Automated backups")}
        description={t(
          "A snapshot is uploaded on this schedule. Building the index never reads Telegram history, so it stays cheap.",
        )}
      >
        <SettingsRow
          label={t("Automated backups")}
          description={t(
            "Snapshots of the drive index, stored in Telegram. The index is what maps your files to the messages that hold them, so it is the one thing a database loss cannot be recovered from without a backup.",
          )}
        >
          <div className="flex items-center justify-end gap-3">
            {config.isPending ? <Spinner size="sm" /> : null}
            <Switch
              aria-label={t("Automated backups")}
              isSelected={enabled}
              isDisabled={busy}
              onChange={(isSelected) => save.mutate({ enabled: isSelected })}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Label className="sr-only">{t("Automated backups")}</Label>
              </Switch.Content>
            </Switch>
          </div>
        </SettingsRow>

        <SettingsRow label={t("Interval")}>
          <Select
            aria-label={t("Interval")}
            className="w-52"
            selectedKey={settings?.interval ?? ""}
            isDisabled={busy || !enabled}
            onSelectionChange={(key) => save.mutate({ interval: String(key) })}
          >
            <Label className="sr-only">{t("Interval")}</Label>
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                {intervalOptions(settings?.interval).map((option) => (
                  <ListBox.Item key={option.key} id={option.key} textValue={t(option.label)}>
                    {t(option.label)}
                  </ListBox.Item>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </SettingsRow>

        <SettingsRow label={t("Last backup")}>
          <div className="flex items-center justify-end gap-3">
            {settings?.lastBackupAt ? (
              <Chip color="success" variant="tertiary">
                {formatMoment(settings.lastBackupAt)}
              </Chip>
            ) : (
              <Chip color="warning" variant="tertiary">
                {t("Never")}
              </Chip>
            )}
          </div>
        </SettingsRow>

        <SettingsRow
          label={t("Back up now")}
          description={t("A snapshot has been queued and appears here once the worker finishes.")}
        >
          <div className="flex items-center justify-end gap-3">
            {run.isPending ? <Spinner size="sm" /> : null}
            <Button
              variant="secondary"
              isDisabled={busy || run.isPending}
              onPress={() => run.mutate()}
            >
              {t("Back up now")}
            </Button>
          </div>
        </SettingsRow>
      </SettingsSection>

      <SettingsSection
        title={t("Snapshots")}
        description={t(
          "Snapshots are stored in a Telegram channel; downloading one keeps a copy outside it.",
        )}
      >
        <SettingsRow
          label={t("Scan the channel")}
          description={t(
            "Scanning walks the backup channel in Telegram and is the only operation here that reads history.",
          )}
        >
          <div className="flex items-center justify-end gap-3">
            {scan.isPending ? <Spinner size="sm" /> : null}
            <Button
              variant="secondary"
              isDisabled={scan.isPending || restore.isPending}
              onPress={() => scan.mutate()}
            >
              <ScanIcon className="size-4" />
              {scan.isPending ? t("Scanning") : t("Scan the channel")}
            </Button>
          </div>
        </SettingsRow>

        {listed.length === 0 ? (
          <SettingsRow label={t("Snapshots")}>
            <div className="flex justify-end">
              <Typography.Paragraph className="text-xs text-muted">
                {t("No snapshots found in the backup channel.")}
              </Typography.Paragraph>
            </div>
          </SettingsRow>
        ) : (
          listed.map((snapshot) => (
            <SettingsRow
              key={snapshot.messageId}
              label={formatMoment(snapshot.createdAt)}
              description={
                snapshot.size === undefined
                  ? undefined
                  : `${formatBytes(snapshot.size)} 路 #${snapshot.messageId}`
              }
            >
              <div className="flex items-center justify-end gap-3">
                <Button
                  variant="tertiary"
                  isDisabled={restore.isPending}
                  onPress={() => window.open(snapshotDownloadUrl(snapshot.messageId), "_blank")}
                >
                  <DownloadIcon className="size-4" />
                  {t("Download")}
                </Button>
                <Button
                  variant="secondary"
                  isDisabled={restore.isPending}
                  onPress={() => {
                    setAcknowledged(false);
                    setPendingRestore(snapshot);
                  }}
                >
                  {t("Restore")}
                </Button>
              </div>
            </SettingsRow>
          ))
        )}
      </SettingsSection>

      <SettingsSection
        title={t("Restore behaviour")}
        description={t("Restore is never automatic. Nothing but this button applies a snapshot.")}
      >
        <SettingsRow
          label={t("Restore behaviour")}
          description={
            mode === "merge"
              ? t("Merge only adds what is missing and never deletes anything.")
              : t(
                  "Overwrite deletes everything that is not in the snapshot and rebuilds the drive from it.",
                )
          }
        >
          <Select
            aria-label={t("Restore behaviour")}
            className="w-52"
            selectedKey={mode}
            isDisabled={busy}
            onSelectionChange={(key) => save.mutate({ restoreMode: String(key) as RestoreMode })}
          >
            <Label className="sr-only">{t("Restore behaviour")}</Label>
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                {(settings?.restoreModes ?? (["overwrite", "merge"] as RestoreMode[])).map(
                  (value) => (
                    <ListBox.Item key={value} id={value} textValue={t(MODE_LABELS[value])}>
                      {t(MODE_LABELS[value])}
                    </ListBox.Item>
                  ),
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </SettingsRow>
      </SettingsSection>

      <Modal.Backdrop
        isOpen={pendingRestore !== null}
        onOpenChange={(open) => {
          if (!open) {
            setPendingRestore(null);
            setAcknowledged(false);
          }
        }}
        isDismissable={!restore.isPending}
      >
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>{t("Restore this snapshot?")}</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="flex flex-col gap-4 py-4">
              <Typography.Paragraph className="text-sm text-muted">
                {t("Restoring rewrites the whole drive and cannot be undone.")}
              </Typography.Paragraph>
              <Typography.Paragraph className="text-sm">
                {pendingRestore ? formatMoment(pendingRestore.createdAt) : ""} 路{" "}
                {t(MODE_LABELS[mode])}
              </Typography.Paragraph>
              <Typography.Paragraph className="text-xs leading-relaxed text-muted">
                {mode === "merge"
                  ? t("Merge only adds what is missing and never deletes anything.")
                  : t(
                      "Overwrite deletes everything that is not in the snapshot and rebuilds the drive from it.",
                    )}
              </Typography.Paragraph>
              {mode === "overwrite" ? (
                <div className="flex items-start gap-3">
                  <Switch
                    aria-label={t(
                      "I understand that overwrite deletes files that are not in the snapshot.",
                    )}
                    isSelected={acknowledged}
                    onChange={setAcknowledged}
                  >
                    <Switch.Content>
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                      <Label className="text-xs leading-relaxed">
                        {t(
                          "I understand that overwrite deletes files that are not in the snapshot.",
                        )}
                      </Label>
                    </Switch.Content>
                  </Switch>
                </div>
              ) : null}
              <div className="flex justify-end gap-3">
                <Button
                  variant="tertiary"
                  isDisabled={restore.isPending}
                  onPress={() => {
                    setPendingRestore(null);
                    setAcknowledged(false);
                  }}
                >
                  {t("Cancel")}
                </Button>
                <Button
                  variant="danger"
                  isDisabled={
                    restore.isPending ||
                    pendingRestore === null ||
                    (mode === "overwrite" && !acknowledged)
                  }
                  onPress={() => {
                    if (pendingRestore) restore.mutate(pendingRestore);
                  }}
                >
                  {restore.isPending ? t("Restoring") : t("Restore")}
                </Button>
              </div>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </>
  );
}

/** lastSnapshot turns the recorded last backup into a one-row list, so the page
 * shows something useful before anyone has ever scanned the channel. */
function lastSnapshot(
  settings:
    | { lastMessageId?: number; channelId?: number; lastBackupAt?: string; lastSize?: number }
    | undefined,
): BackupSnapshot[] {
  if (!settings?.lastMessageId || !settings.lastBackupAt) return [];
  return [
    {
      messageId: settings.lastMessageId,
      channelId: settings.channelId ?? 0,
      createdAt: settings.lastBackupAt,
      size: settings.lastSize,
    },
  ];
}

/** intervalOptions keeps the stored interval visible even when it is not one of
 * the presets, rather than letting the Select render blank. */
function intervalOptions(current: string | undefined): { key: string; label: string }[] {
  if (!current || INTERVAL_OPTIONS.some((option) => option.key === current)) {
    return INTERVAL_OPTIONS;
  }
  return [{ key: current, label: current }, ...INTERVAL_OPTIONS];
}

function formatMoment(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 10 || unit === 0 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`;
}
