import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * The operating-system integration endpoints sit outside the generated OpenAPI
 * contract (they change the machine, not the database), so they go through the
 * raw fetch helper. `apiFetch` prefixes the `/api` base and normalizes failures
 * exactly like the generated client.
 */
const AUTOSTART_PATH = "/system/autostart";
const MOUNT_PATH = "/system/webdav-mount";

const autostartSchema = z.object({
  supported: z.boolean(),
  enabled: z.boolean(),
  command: z.string().optional(),
  detail: z.string().optional(),
});

export type AutostartStatus = z.infer<typeof autostartSchema>;

const prerequisiteItemSchema = z.object({
  key: z.string(),
  ok: z.boolean(),
  current: z.string().optional(),
  required: z.string().optional(),
  description: z.string(),
});

const prerequisitesSchema = z.object({
  ready: z.boolean(),
  items: z.array(prerequisiteItemSchema),
  fixCommand: z.string(),
  needsAdmin: z.boolean(),
});

const mountSchema = z.object({
  supported: z.boolean(),
  mounted: z.boolean(),
  drive: z.string().optional(),
  url: z.string().optional(),
  detail: z.string().optional(),
  message: z.string().optional(),
  prerequisites: prerequisitesSchema,
});

export type PrerequisiteItem = z.infer<typeof prerequisiteItemSchema>;
export type MountStatus = z.infer<typeof mountSchema>;

async function parse<T>(response: Response, schema: z.ZodType<T>): Promise<T> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = schema.safeParse(payload);
  if (!result.success) throw invalidResponse(t("The system integration response is malformed."));
  return result.data;
}

export async function fetchAutostart(): Promise<AutostartStatus> {
  return parse(await apiFetch(AUTOSTART_PATH), autostartSchema);
}

export async function updateAutostart(enabled: boolean): Promise<AutostartStatus> {
  return parse(
    await apiFetch(AUTOSTART_PATH, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
    autostartSchema,
  );
}

export async function fetchMountStatus(): Promise<MountStatus> {
  return parse(await apiFetch(MOUNT_PATH), mountSchema);
}

/** Mounts the DAV tree, optionally asking Windows for the elevation it needs. */
export async function createMount(elevate: boolean): Promise<MountStatus> {
  return parse(
    await apiFetch(MOUNT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ elevate }),
    }),
    mountSchema,
  );
}

export async function deleteMount(): Promise<MountStatus> {
  return parse(await apiFetch(MOUNT_PATH, { method: "DELETE" }), mountSchema);
}

export function autostartQueryOptions() {
  return queryOptions({ queryKey: queryKeys.autostart, queryFn: fetchAutostart });
}

export function mountQueryOptions() {
  return queryOptions({ queryKey: queryKeys.webdavMount, queryFn: fetchMountStatus });
}
