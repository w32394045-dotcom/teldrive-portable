import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * Local-network access is served by its own handler rather than the generated
 * OpenAPI contract, so it goes through the raw fetch helper. `apiFetch` prefixes
 * the `/api` base, attaches the session and normalizes failures exactly like the
 * generated client.
 */
const DEVICES_PATH = "/lan/devices";
const NETWORK_PATH = "/lan/network";
const STATUS_PATH = "/system/status";

const deviceSchema = z.object({
  id: z.string(),
  name: z.string(),
  address: z.string().optional(),
  state: z.enum(["pending", "approved", "denied"]),
  createdAt: z.string(),
  decidedAt: z.string().optional(),
  lastSeenAt: z.string().optional(),
});

export type LanDevice = z.infer<typeof deviceSchema>;
export type LanDeviceState = LanDevice["state"];

const devicesSchema = z.object({
  devices: z.array(deviceSchema),
  pending: z.array(deviceSchema),
  networkEnabled: z.boolean(),
  restartRequired: z.boolean(),
  requestPath: z.string(),
  pollPath: z.string(),
});

export type LanStatus = z.infer<typeof devicesSchema>;

async function parse<T>(response: Response, schema: z.ZodType<T>, malformed: string): Promise<T> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = schema.safeParse(payload);
  if (!result.success) throw invalidResponse(t(malformed));
  return result.data;
}

export async function fetchLanStatus(): Promise<LanStatus> {
  return parse(
    await apiFetch(DEVICES_PATH),
    devicesSchema,
    "The local-network response is malformed.",
  );
}

/** Approves, refuses or forgets one device, returning the whole new state. */
export async function decideDevice(
  id: string,
  action: "approve" | "deny" | "forget",
): Promise<LanStatus> {
  const path =
    action === "forget"
      ? `${DEVICES_PATH}/${encodeURIComponent(id)}`
      : `${DEVICES_PATH}/${encodeURIComponent(id)}/${action}`;
  return parse(
    await apiFetch(path, { method: action === "forget" ? "DELETE" : "POST" }),
    devicesSchema,
    "The local-network response is malformed.",
  );
}

/**
 * Turns network reachability on or off. It takes effect at the next start: a
 * listening socket cannot be widened underneath the connections already on it.
 */
export async function setNetworkEnabled(enabled: boolean): Promise<LanStatus> {
  return parse(
    await apiFetch(NETWORK_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
    devicesSchema,
    "The local-network response is malformed.",
  );
}

export function lanQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.lanAccess,
    queryFn: fetchLanStatus,
    // Requests arrive while the page is open, so the list has to keep up.
    refetchInterval: 5000,
  });
}

/**
 * Where this drive actually is. The addresses are computed by the server from
 * its own interfaces, which is the only place that knows them: a browser cannot
 * see the machine's LAN address, and hard-coding one would be a lie the moment
 * the machine changes networks.
 */
const statusSchema = z.object({
  address: z.string(),
  url: z.string(),
  networkUrl: z.string().optional(),
  networkEnabled: z.boolean(),
  boundToNetwork: z.boolean(),
  pendingDevices: z.number(),
});

export type SystemStatus = z.infer<typeof statusSchema>;

export async function fetchSystemStatus(): Promise<SystemStatus> {
  return parse(
    await apiFetch(STATUS_PATH),
    statusSchema,
    "The local-network response is malformed.",
  );
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
