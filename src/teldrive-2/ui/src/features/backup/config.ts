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

const restoreModeSchema = z.enum(["overwrite", "merge"]);

export type RestoreMode = z.infer<typeof restoreModeSchema>;

const backupConfigSchema = z.object({
  enabled: z.boolean(),
  interval: z.string(),
  channelId: z.number().optional(),
  lastMessageId: z.number().optional(),
  lastSize: z.number().optional(),
  lastBackupAt: z.string().optional(),
  restoreMode: restoreModeSchema,
  restoreModes: z.array(restoreModeSchema).min(1),
  defaultInterval: z.string(),
  minimumInterval: z.string(),
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
  durationMs: z.number(),
});

export type RestoreResult = z.infer<typeof restoreResultSchema>;

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
  changes: Partial<Pick<BackupConfig, "enabled" | "interval" | "restoreMode">>,
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
export async function restoreBackup(messageId: number, mode: RestoreMode): Promise<RestoreResult> {
  return parse(
    await apiFetch(BACKUP_RESTORE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messageId, mode, confirm: "restore" }),
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
