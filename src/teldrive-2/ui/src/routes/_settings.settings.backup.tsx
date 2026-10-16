import {
  Accordion,
  Button,
  Chip,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  Switch,
  TextField,
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
  inspectBackup,
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
  const [passphrase, setPassphrase] = useState("");
  const [restorePassphrase, setRestorePassphrase] = useState("");
  const [needsRestorePassphrase, setNeedsRestorePassphrase] = useState(false);

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
      restoreBackup(snapshot.messageId, config.data?.restoreMode ?? "overwrite", restorePassphrase),
    onSuccess: (result) => {
      setPendingRestore(null);
      setAcknowledged(false);
      toast.success(t("The drive was restored."), {
        description: t(
          "Files {{files}} · folders {{folders}} · parts {{parts}} · deleted {{deleted}}",
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
      // A sealed snapshot the stored passphrase does not open comes back asking
      // for one. Rather than failing the restore, reopen the dialog with the
      // passphrase field, which is the only place the user can fix it.
      if (userMessage(error).toLowerCase().includes("password")) {
        setNeedsRestorePassphrase(true);
        toast.error(t("This snapshot needs its passphrase"), {
          description: t("Type the passphrase this snapshot was sealed with, then restore again."),
        });
        return;
      }
      toast.error(t("The snapshot could not be restored"), { description: userMessage(error) });
    },
  });

  /**
   * Inspecting before restoring is what turns "this snapshot is encrypted" from
   * a failure at the last moment into a question asked while the passphrase is
   * still at hand.
   */
  const inspect = useMutation({
    mutationFn: (snapshot: BackupSnapshot) => inspectBackup(snapshot.messageId, restorePassphrase),
    onSuccess: (summary) => {
      // It opened, so whatever passphrase was used is the right one.
      setNeedsRestorePassphrase(false);
      toast.success(t("Snapshot opened"), {
        description: t("{{files}} files · {{folders}} folders · {{parts}} parts", {
          files: summary.counts.files,
          folders: summary.counts.folders,
          parts: summary.counts.parts,
        }),
      });
    },
    onError: (error) => {
      setNeedsRestorePassphrase(true);
      toast.error(t("This snapshot needs its passphrase"), { description: userMessage(error) });
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
                  : `${formatBytes(snapshot.size)} · #${snapshot.messageId}`
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
        title={t("Sealing")}
        description={t(
          "A snapshot is the one file that can rebuild the whole drive. With a passphrase it is stored as ciphertext, so the channel it sits in is not enough to read it.",
        )}
      >
        <SettingsRow
          label={t("Passphrase")}
          description={t(
            "At least {{count}} characters. Leave it empty to store snapshots as plain JSON.",
            {
              count: settings?.minimumPasswordLength ?? 8,
            },
          )}
          align="start"
        >
          <div className="flex w-full flex-col items-end gap-3">
            <div className="flex w-full items-center justify-end gap-3">
              {settings?.passwordSet ? (
                <Chip color="success" variant="tertiary">
                  {t("Enabled")}
                </Chip>
              ) : (
                <Chip color="warning" variant="tertiary">
                  {t("Disabled")}
                </Chip>
              )}
              <TextField className="w-64">
                <Label className="sr-only">{t("Passphrase")}</Label>
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={passphrase}
                  placeholder={settings?.passwordSet ? "••••••••" : t("Passphrase")}
                  onChange={(event) => setPassphrase(event.currentTarget.value)}
                />
              </TextField>
            </div>
            <div className="flex items-center gap-3">
              <Button
                variant="tertiary"
                isDisabled={busy}
                onPress={() => {
                  // The alphabet behind this leaves out the characters people
                  // misread when copying a key by hand.
                  setPassphrase(randomPassphrase(20));
                }}
              >
                {t("Generate")}
              </Button>
              <Button
                variant="secondary"
                isDisabled={busy || passphrase.length === 0}
                onPress={() => {
                  save.mutate({ password: passphrase });
                  setPassphrase("");
                }}
              >
                {t("Save passphrase")}
              </Button>
              {settings?.passwordSet ? (
                <Button
                  variant="tertiary"
                  isDisabled={busy}
                  onPress={() => save.mutate({ password: "" })}
                >
                  {t("Stop sealing")}
                </Button>
              ) : null}
            </div>
            {settings?.content.encryptionKeys && !settings.passwordSet ? (
              <Typography.Paragraph className="text-right text-xs leading-relaxed text-warning">
                {t(
                  "The encryption keys are switched on and no passphrase is set, so they are stored in the clear. Anyone who can read the backup channel can decrypt every file.",
                )}
              </Typography.Paragraph>
            ) : null}
          </div>
        </SettingsRow>

        <SettingsRow label={t("Snapshot contents")} align="start">
          <div className="w-full">
            <Accordion hideSeparator className="w-full">
              <Accordion.Item id="backup-contents">
                <Accordion.Heading>
                  <Accordion.Trigger className="rounded-lg text-xs font-medium text-muted hover:text-foreground">
                    {selectedContentLabels(
                      settings?.content ?? { encryptionKeys: false, users: false },
                    )}
                    <Accordion.Indicator />
                  </Accordion.Trigger>
                </Accordion.Heading>
                <Accordion.Panel>
                  <Accordion.Body>
                    <div className="flex flex-col gap-4 border-border border-t pt-4">
                      <Switch
                        aria-label={t("Encryption keys")}
                        isSelected={settings?.content.encryptionKeys ?? false}
                        isDisabled={busy || settings?.secretsAvailable === false}
                        onChange={(isSelected) =>
                          save.mutate({ content: { encryptionKeys: isSelected } })
                        }
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                          <Label className="text-xs leading-relaxed">{t("Encryption keys")}</Label>
                        </Switch.Content>
                      </Switch>
                      <Typography.Paragraph className="text-xs leading-relaxed text-muted">
                        {t(
                          "The keys that decrypt stored parts live in config.toml, not in the database, so a database loss loses them. Without them an encrypted drive survives as ciphertext nobody can read.",
                        )}
                      </Typography.Paragraph>
                      <Switch
                        aria-label={t("Accounts")}
                        isSelected={settings?.content.users ?? false}
                        isDisabled={busy}
                        onChange={(isSelected) => save.mutate({ content: { users: isSelected } })}
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                          <Label className="text-xs leading-relaxed">{t("Accounts")}</Label>
                        </Switch.Content>
                      </Switch>
                      <Typography.Paragraph className="text-xs leading-relaxed text-muted">
                        {t(
                          "The account rows the catalog references. Rebuilding on a fresh database needs them before any file can be put back. An account that already exists keeps its role.",
                        )}
                      </Typography.Paragraph>
                    </div>
                  </Accordion.Body>
                </Accordion.Panel>
              </Accordion.Item>
            </Accordion>
          </div>
        </SettingsRow>
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
                {pendingRestore ? formatMoment(pendingRestore.createdAt) : ""} ·{" "}
                {t(MODE_LABELS[mode])}
              </Typography.Paragraph>
              <Typography.Paragraph className="text-xs leading-relaxed text-muted">
                {mode === "merge"
                  ? t("Merge only adds what is missing and never deletes anything.")
                  : t(
                      "Overwrite deletes everything that is not in the snapshot and rebuilds the drive from it.",
                    )}
              </Typography.Paragraph>
              {needsRestorePassphrase ? (
                <TextField>
                  <Label className="text-xs">{t("Passphrase")}</Label>
                  <Input
                    type="password"
                    autoComplete="off"
                    value={restorePassphrase}
                    onChange={(event) => setRestorePassphrase(event.currentTarget.value)}
                  />
                  <Typography.Paragraph className="mt-1 text-xs leading-relaxed text-muted">
                    {t("Type the passphrase this snapshot was sealed with, then restore again.")}
                  </Typography.Paragraph>
                </TextField>
              ) : null}
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
              {inspect.data ? (
                <div className="rounded-lg border border-border p-3">
                  <Typography.Paragraph className="text-xs leading-relaxed text-muted">
                    {t("{{files}} files · {{folders}} folders · {{parts}} parts", {
                      files: inspect.data.counts.files,
                      folders: inspect.data.counts.folders,
                      parts: inspect.data.counts.parts,
                    })}
                  </Typography.Paragraph>
                  <Typography.Paragraph className="mt-1 text-xs leading-relaxed text-muted">
                    {inspect.data.secretsIncluded ? t("Encryption keys") : t("Catalog only")}
                    {" · "}
                    {inspect.data.users > 0 ? t("Accounts") : t("Catalog only")}
                  </Typography.Paragraph>
                </div>
              ) : null}
              <div className="flex justify-end gap-3">
                <Button
                  variant="tertiary"
                  isDisabled={inspect.isPending || restore.isPending || pendingRestore === null}
                  onPress={() => {
                    if (pendingRestore) inspect.mutate(pendingRestore);
                  }}
                >
                  {inspect.isPending ? t("Scanning") : t("Inspect")}
                </Button>
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

/** selectedContentLabels summarises the accordion in its collapsed state, so the
 * choice is visible without opening it. */
function selectedContentLabels(content: { encryptionKeys: boolean; users: boolean }): string {
  const chosen: string[] = [];
  if (content?.encryptionKeys) chosen.push(t("Encryption keys"));
  if (content?.users) chosen.push(t("Accounts"));
  return chosen.length > 0 ? chosen.join(" · ") : t("Catalog only");
}

/** randomPassphrase mirrors the server's generator. The alphabet leaves out
 * 0/O and 1/l/I, which are the characters people misread when copying a key by
 * hand, and the length sits inside the range the server accepts. */
const PASSPHRASE_ALPHABET = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";

function randomPassphrase(length: number): string {
  const bytes = new Uint32Array(length);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const value of bytes) {
    out += PASSPHRASE_ALPHABET[value % PASSPHRASE_ALPHABET.length];
  }
  return out;
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
