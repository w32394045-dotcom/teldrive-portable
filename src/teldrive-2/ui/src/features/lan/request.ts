/**
 * Asking the person at the drive for permission.
 *
 * A device that reaches this drive over the network holds nothing: no session,
 * no API key, no approval. This module is the whole conversation from that side
 * -- ask, wait, then keep the credential the drive hands back.
 *
 * The credential lives in localStorage rather than sessionStorage because it is
 * meant to outlive a browser restart: the person at the drive approved this
 * machine once, and asking them again every time a tab closes is how an access
 * control becomes one people turn off.
 */
import { z } from "zod";

const REQUEST_PATH = "/api/lan/request";
const DEVICE_TOKEN_KEY = "teldrive.deviceToken";

/** The header that carries the credential. Keep in step with deviceHeader. */
export const DEVICE_HEADER = "X-Teldrive-Device";

/** How often to ask whether the person has answered yet. */
export const POLL_INTERVAL_MS = 2000;

const requestSchema = z.object({
  id: z.string(),
  state: z.enum(["pending", "approved", "denied"]),
  message: z.string().optional(),
});

export type AccessRequest = z.infer<typeof requestSchema>;

const claimSchema = z.object({
  state: z.enum(["pending", "approved", "denied"]),
  token: z.string().optional(),
});

export type AccessClaim = z.infer<typeof claimSchema>;

export function readDeviceToken(): string | null {
  try {
    const value = window.localStorage.getItem(DEVICE_TOKEN_KEY);
    return value?.trim() ? value.trim() : null;
  } catch {
    return null;
  }
}

export function writeDeviceToken(token: string) {
  try {
    window.localStorage.setItem(DEVICE_TOKEN_KEY, token.trim());
  } catch {
    // Ignore: the token still works for this page view, and the person can ask
    // again if the browser refuses to keep it.
  }
}

export function clearDeviceToken() {
  try {
    window.localStorage.removeItem(DEVICE_TOKEN_KEY);
  } catch {
    // Ignore.
  }
}

/** A name the person at the drive will recognise in the list. */
export function suggestedDeviceName(): string {
  const agent = navigator.userAgent;
  // The order matters: an iPad reports itself as a Mac, so the mobile markers
  // are checked first.
  if (/iPhone|iPad|iPod/i.test(agent)) return "iPhone or iPad";
  if (/Android/i.test(agent)) return "Android device";
  if (/Windows/i.test(agent)) return "Windows computer";
  if (/Macintosh|Mac OS X/i.test(agent)) return "Mac";
  if (/Linux/i.test(agent)) return "Linux computer";
  return "A device on this network";
}

/** Asks for access. A repeat from the same address returns the same request. */
export async function requestAccess(name: string): Promise<AccessRequest> {
  const response = await fetch(REQUEST_PATH, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  const payload: unknown = await response.json().catch(() => undefined);
  const parsed = requestSchema.safeParse(payload);
  if (!parsed.success) {
    throw new Error(
      response.ok
        ? "The drive sent an answer this page cannot read."
        : `The request failed (${response.status}).`,
    );
  }
  return parsed.data;
}

/**
 * Checks whether the person has answered. The credential is handed over exactly
 * once, so a poll that returns it is the only one that ever will.
 */
export async function pollAccess(id: string): Promise<AccessClaim> {
  const response = await fetch(`${REQUEST_PATH}/${encodeURIComponent(id)}`);
  const payload: unknown = await response.json().catch(() => undefined);
  const parsed = claimSchema.safeParse(payload);
  if (!parsed.success) {
    // A request that expired or was never there is not a failure to retry
    // forever: the device has to start again.
    throw new Error("This request is no longer waiting.");
  }
  return parsed.data;
}
