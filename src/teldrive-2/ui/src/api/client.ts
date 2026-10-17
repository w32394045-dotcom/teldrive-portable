import createFetchClient from "openapi-fetch";
import createQueryClient from "openapi-react-query";
import { readApiKey } from "@/auth/api-key";
import { PERSIST_HEADER, persistHeaderValue } from "@/auth/keep-signed-in";
import { DEVICE_HEADER, readDeviceToken } from "@/features/lan/request";
import { normalizeApiError } from "./errors";
import type { paths } from "./schema";

const API_BASE_URL = "/api";

/**
 * Attach the API key when one is stored, and state the "keep me signed in"
 * choice.
 *
 * Every request goes through `fetchWithApiErrors`, so setting both here covers
 * the generated client and hand-written `apiFetch` calls alike -- including the
 * login that issues the cookies, which is the one that actually needs the
 * preference. Explicit caller-supplied values win.
 *
 * The generated client does not call `fetch` with a plain init: it builds a
 * `Request` (with `Content-Type`, `Idempotency-Key` and every other header the
 * contract defines) and passes that plus no init at all. Any init given to
 * `fetch` replaces the request's headers wholesale, so the request's own headers
 * have to be copied over before ours are added -- otherwise every generated call
 * would go out with no `Content-Type` and no `Idempotency-Key`, and the server
 * would reject it as malformed.
 */
function withCredentials(input: RequestInfo | URL, init?: RequestInit): RequestInit {
  const headers = new Headers(init?.headers);
  if (input instanceof Request) {
    input.headers.forEach((value, name) => {
      if (!headers.has(name)) headers.set(name, value);
    });
  }
  const key = readApiKey();
  if (key && !headers.has("X-Api-Key")) headers.set("X-Api-Key", key);
  // An approved device carries its own credential, which the server turns into
  // this machine's session. It goes out with every call because every call may
  // be the first one after a restart.
  const device = readDeviceToken();
  if (device && !headers.has(DEVICE_HEADER)) headers.set(DEVICE_HEADER, device);
  if (!headers.has(PERSIST_HEADER)) headers.set(PERSIST_HEADER, persistHeaderValue());
  return { ...init, headers };
}

async function fetchWithApiErrors(input: RequestInfo | URL, init?: RequestInit) {
  let response: Response;
  try {
    response = await fetch(input, withCredentials(input, init));
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    throw normalizeApiError(error);
  }

  if (response.ok) return response;

  let body: unknown;
  try {
    body = await response.clone().json();
  } catch {
    body = undefined;
  }
  throw normalizeApiError(body, response);
}

export const fetchClient = createFetchClient<paths>({
  baseUrl: API_BASE_URL,
  fetch: fetchWithApiErrors,
});

export const $api = createQueryClient(fetchClient);

export function apiFetch(path: string, init?: RequestInit) {
  return fetchWithApiErrors(`${API_BASE_URL}${path}`, init);
}
