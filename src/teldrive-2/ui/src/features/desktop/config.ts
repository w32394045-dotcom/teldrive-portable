import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * Saving a file to the computer that runs the drive.
 *
 * This is not a browser download: the browser saves where its own settings say,
 * and a page cannot change that. The native shell can, so when the drive is
 * served by it the server offers these endpoints: the folder is a preference of
 * this computer, and the folder picker is a dialog on this computer's screen.
 */
const DOWNLOADS_PATH = "/desktop/downloads";
const FOLDER_PATH = "/desktop/folder";
const SAVE_PATH = "/desktop/save";

const settingsSchema = z.object({
  downloadDir: z.string(),
  askEveryTime: z.boolean(),
  canChoose: z.boolean(),
  defaultDir: z.string().optional(),
});

export type DesktopSettings = z.infer<typeof settingsSchema>;

const saveSchema = z.object({
  path: z.string(),
  name: z.string(),
  size: z.number(),
});

export type SavedFile = z.infer<typeof saveSchema>;

async function parse<T>(response: Response, schema: z.ZodType<T>): Promise<T> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = schema.safeParse(payload);
  if (!result.success) throw invalidResponse(t("The system integration response is malformed."));
  return result.data;
}

export async function fetchDesktopSettings(): Promise<DesktopSettings> {
  return parse(await apiFetch(DOWNLOADS_PATH), settingsSchema);
}

/** Changes the folder and the ask-every-time switch, returning the stored state. */
export async function updateDesktopSettings(patch: {
  downloadDir?: string;
  askEveryTime?: boolean;
}): Promise<DesktopSettings> {
  return parse(
    await apiFetch(DOWNLOADS_PATH, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
    settingsSchema,
  );
}

/** Opens the folder picker on the computer that runs the drive. */
export async function chooseDownloadFolder(): Promise<string> {
  const result = await parse(
    await apiFetch(FOLDER_PATH, { method: "POST" }),
    z.object({ path: z.string() }),
  );
  return result.path;
}

/** Writes one file from the drive into the configured folder of this computer. */
export async function saveFileToComputer(fileId: string): Promise<SavedFile> {
  return parse(
    await apiFetch(SAVE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ fileId }),
    }),
    saveSchema,
  );
}

export function desktopSettingsQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.desktopSettings,
    queryFn: fetchDesktopSettings,
  });
}
