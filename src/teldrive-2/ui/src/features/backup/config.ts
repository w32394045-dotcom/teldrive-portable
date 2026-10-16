import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * The snapshot settings are served by the backup handler, not by the generated
 * OpenAPI contract, so they go through the raw fetch helper. `apiFetch` prefixes
 * the `/api` base, attaches the session and normalizes failures exactly like the
 * generated client.
 */
const BACKUP_CONFIG_PATH = "/backup-config";
const BACKUP_RUN_PATH = "/backup/run";
const BACKUP_SCAN_PATH = "/backup/scan";
const BACKUP_RESTORE_PATH = "/backup/restore";
const BACKUP_SNAPSHOT_PATH = "/backup/snapshot";
const BACKUP_INSPECT_PATH = "/backup/inspect";

const restoreModeSchema = z.enum(["overwrite", "merge"]);

export type RestoreMode = z.infer<typeof restoreModeSchema>;

const contentSchema = z.object({
  encryptionKeys: z.boolean(),
  users: z.boolean(),
});

export type BackupContent = z.infer<typeof contentSchema>;

const backupConfigSchema = z.object({
  enabled: z.boolean(),
  interval: z.string(),
  channelId: z.number().optional(),
  lastMessageId: z.number().optional(),
  lastSize: z.number().optional(),
  lastBackupAt: z.string().optional(),
  restoreMode: restoreModeSchema,
  restoreModes: z.array(restoreModeSchema).min(1),
  passwordSet: z.boolean(),
  content: contentSchema,
  secretsAvailable: z.boolean(),
  defaultInterval: z.string(),
  minimumInterval: z.string(),
  minimumPasswordLength: z.number(),
});

export type BackupConfig = z.infer<typeof backupConfigSchema>;

const snapshotSchema = z.object({
  messageId: z.number(),
  channelId: z.number(),
  createdAt: z.string(),
  name: z.string().optional(),
  size: z.number().optional(),
});

export type BackupSnapshot = z.infer<typeof snapshotSchema>;

const scanSchema = z.object({
  snapshots: z.array(snapshotSchema),
  count: z.number(),
});

const restoreResultSchema = z.object({
  mode: restoreModeSchema,
  channels: z.number(),
  files: z.number(),
  folders: z.number(),
  parts: z.number(),
  skippedFiles: z.number(),
  skippedParts: z.number(),
  deletedFiles: z.number().optional(),
  orphans: z.array(z.string()).optional(),
  users: z.number().optional(),
  secretsIncluded: z.boolean().optional(),
  durationMs: z.number(),
});

export type RestoreResult = z.infer<typeof restoreResultSchema>;

const summarySchema = z.object({
  format: z.string(),
  version: z.number(),
  createdAt: z.string(),
  userId: z.number(),
  counts: z.object({
    files: z.number(),
    folders: z.number(),
    parts: z.number(),
    channels: z.number(),
  }),
  sealed: z.boolean(),
  content: contentSchema,
  users: z.number(),
  secretsIncluded: z.boolean(),
});

export type SnapshotSummary = z.infer<typeof summarySchema>;

async function parse<T>(response: Response, schema: z.ZodType<T>, malformed: string): Promise<T> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = schema.safeParse(payload);
  if (!result.success) throw invalidResponse(t(malformed));
  return result.data;
}

export async function fetchBackupConfig(): Promise<BackupConfig> {
  return parse(
    await apiFetch(BACKUP_CONFIG_PATH),
    backupConfigSchema,
    "The backup settings response is malformed.",
  );
}

/** A partial update: omitted fields keep whatever is stored. */
export async function updateBackupConfig(
  changes: Partial<Pick<BackupConfig, "enabled" | "interval" | "restoreMode">> & {
    /** An empty string turns sealing off; omitting it leaves the stored one. */
    password?: string;
    content?: Partial<BackupContent>;
  },
): Promise<BackupConfig> {
  return parse(
    await apiFetch(BACKUP_CONFIG_PATH, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(changes),
    }),
    backupConfigSchema,
    "The backup settings response is malformed.",
  );
}

/**
 * Describes a snapshot without applying it. A sealed snapshot that the stored
 * passphrase does not open comes back as a password_required failure, which is
 * what lets the page ask for one while the user still has it at hand.
 */
export async function inspectBackup(messageId: number, password = ""): Promise<SnapshotSummary> {
  return parse(
    await apiFetch(BACKUP_INSPECT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messageId, password }),
    }),
    summarySchema,
    "The snapshot description is malformed.",
  );
}

/** Queues a snapshot on the maintenance queue; the page polls for the result. */
export async function runBackupNow(): Promise<{ queued: boolean; jobId: number }> {
  return parse(
    await apiFetch(BACKUP_RUN_PATH, { method: "POST" }),
    z.object({ queued: z.boolean(), jobId: z.number(), userId: z.number() }),
    "The backup response is malformed.",
  );
}

/**
 * Lists what is actually in the backup channel. This is the only call here that
 * walks Telegram history, which is why it is a button and not a page load.
 */
export async function scanBackups(): Promise<BackupSnapshot[]> {
  const result = await parse(
    await apiFetch(BACKUP_SCAN_PATH, { method: "POST" }),
    scanSchema,
    "The scan response is malformed.",
  );
  return result.snapshots;
}

/**
 * Applies a snapshot. The server requires the literal confirmation token, so it
 * is spelled out here rather than being a boolean the caller might pass by
 * accident.
 */
export async function restoreBackup(
  messageId: number,
  mode: RestoreMode,
  password = "",
): Promise<RestoreResult> {
  return parse(
    await apiFetch(BACKUP_RESTORE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messageId, mode, confirm: "restore", password }),
    }),
    restoreResultSchema,
    "The restore response is malformed.",
  );
}

/** The URL that streams one snapshot out, for a plain browser download. */
export function snapshotDownloadUrl(messageId: number): string {
  return `/api${BACKUP_SNAPSHOT_PATH}?messageId=${encodeURIComponent(String(messageId))}`;
}

export function backupConfigQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.backupConfig,
    queryFn: fetchBackupConfig,
  });
}
