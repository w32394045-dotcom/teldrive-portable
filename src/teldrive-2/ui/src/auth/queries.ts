import { queryOptions } from "@tanstack/react-query";
import { fetchClient } from "@/api/client";
import { isUnauthorized, unwrap } from "@/api/errors";

const currentUserQueryKey = ["auth", "current-user"] as const;

export function currentUserQueryOptions() {
  return queryOptions({
    queryKey: currentUserQueryKey,
    queryFn: () => unwrap(fetchClient.GET("/v1/me")),
    staleTime: 30_000,
    gcTime: 10 * 60_000,
    // A rejected credential is an answer, not a blip: retrying it would only
    // delay the login page. Anything else -- the bundle opening the browser
    // before the server is quite ready, a dropped connection, a restart --
    // would otherwise look exactly like being signed out, which is the one
    // thing a session is not supposed to do.
    retry: (failureCount, error) => !isUnauthorized(error) && failureCount < 3,
    retryDelay: (attempt) => Math.min(750 * 2 ** attempt, 4000),
  });
}
