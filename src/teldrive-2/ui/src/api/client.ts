import createFetchClient from "openapi-fetch";
import createQueryClient from "openapi-react-query";
import { readApiKey } from "@/auth/api-key";
import { normalizeApiError } from "./errors";
import type { paths } from "./schema";

const API_BASE_URL = "/api";

/**
 * Attach the API key when one is stored.
 *
 * Every request goes through `fetchWithApiErrors`, so injecting the header here
 * covers the generated client and hand-written `apiFetch` calls alike. The
 * header is set only when absent so an explicit caller-supplied value wins.
 */
function withApiKey(init?: RequestInit): RequestInit {
  const key = readApiKey();
  if (!key) return init ?? {};
  const headers = new Headers(init?.headers);
  if (!headers.has("X-Api-Key")) headers.set("X-Api-Key", key);
  return { ...init, headers };
}

async function fetchWithApiErrors(input: RequestInfo | URL, init?: RequestInit) {
  let response: Response;
  try {
    response = await fetch(input, withApiKey(init));
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
