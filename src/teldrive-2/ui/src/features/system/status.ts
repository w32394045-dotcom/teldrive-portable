import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * Where this drive is and what it can do, as reported by the server about the
 * machine it runs on. The addresses cannot be computed in the browser - it cannot
 * see the machine's interfaces - and `desktop` is what tells the page that it is
 * being served by the native shell, which is the only situation in which saving
 * to this computer is possible at all.
 */
const STATUS_PATH = "/system/status";

const statusSchema = z.object({
  address: z.string(),
  url: z.string(),
  networkUrl: z.string().optional(),
  networkEnabled: z.boolean(),
  boundToNetwork: z.boolean(),
  pendingDevices: z.number(),
  desktop: z.boolean(),
});

export type SystemStatus = z.infer<typeof statusSchema>;

export async function fetchSystemStatus(): Promise<SystemStatus> {
  const response = await apiFetch(STATUS_PATH);
  const payload: unknown = await response.json().catch(() => undefined);
  const result = statusSchema.safeParse(payload);
  if (!result.success) throw invalidResponse(t("The system integration response is malformed."));
  return result.data;
}

export function systemStatusQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.systemStatus,
    queryFn: fetchSystemStatus,
    // The address only changes when the machine changes networks, but that is
    // exactly the moment somebody has this page open and is reading it.
    refetchInterval: 30_000,
  });
}
