import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * The enable/disable toggle is served by the WebDAV handler, not by the
 * generated OpenAPI contract, so it is requested through the raw fetch helper.
 * `apiFetch` prefixes the `/api` base and normalizes failures exactly like the
 * generated `$api` client, which keeps the browser session and error handling
 * identical to every other UI call.
 */
const WEBDAV_CONFIG_PATH = "/webdav-config";

const webdavConfigSchema = z.object({
  enabled: z.boolean(),
  url: z.string(),
});

export type WebDAVConfig = z.infer<typeof webdavConfigSchema>;

async function parseConfig(response: Response): Promise<WebDAVConfig> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = webdavConfigSchema.safeParse(payload);
  if (!result.success) throw invalidResponse(t("The WebDAV settings response is malformed."));
  return result.data;
}

export async function fetchWebDAVConfig(): Promise<WebDAVConfig> {
  return parseConfig(await apiFetch(WEBDAV_CONFIG_PATH));
}

export async function updateWebDAVConfig(enabled: boolean): Promise<WebDAVConfig> {
  return parseConfig(
    await apiFetch(WEBDAV_CONFIG_PATH, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  );
}

export function webdavConfigQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.webdavConfig,
    queryFn: fetchWebDAVConfig,
  });
}
