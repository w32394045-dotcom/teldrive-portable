import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { apiFetch } from "@/api/client";
import { invalidResponse } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { t } from "@/i18n";

/**
 * The settings vault is served by its own handler rather than the generated
 * OpenAPI contract, so it goes through the raw fetch helper. `apiFetch` prefixes
 * the `/api` base, attaches the session and normalizes failures exactly like the
 * generated client.
 */
const VAULT_PATH = "/vault";
const VAULT_FORGET_PATH = "/vault/forget";

const vaultStatusSchema = z.object({
  exists: z.boolean(),
  mode: z.string(),
  sealedAt: z.string().optional(),
  keyLength: z.number().optional(),
  secrets: z.array(z.string()),
  minimumKeyLength: z.number(),
  maximumKeyLength: z.number(),
});

export type VaultStatus = z.infer<typeof vaultStatusSchema>;

const createResponseSchema = z.object({
  status: vaultStatusSchema,
  key: z.string().optional(),
  remembered: z.boolean(),
});

export type VaultCreation = z.infer<typeof createResponseSchema>;

export type VaultKeyMode = "saved" | "prompt";

async function parse<T>(response: Response, schema: z.ZodType<T>, malformed: string): Promise<T> {
  const payload: unknown = await response.json().catch(() => undefined);
  const result = schema.safeParse(payload);
  if (!result.success) throw invalidResponse(t(malformed));
  return result.data;
}

export async function fetchVaultStatus(): Promise<VaultStatus> {
  return parse(await apiFetch(VAULT_PATH), vaultStatusSchema, "The vault response is malformed.");
}

/**
 * Seals the instance's secrets. An empty key asks the server to generate one,
 * which is then returned exactly once -- the only moment it can be written down.
 */
export async function createVault(key: string, mode: VaultKeyMode): Promise<VaultCreation> {
  return parse(
    await apiFetch(VAULT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key, mode }),
    }),
    createResponseSchema,
    "The vault response is malformed.",
  );
}

/** Drops the remembered key, so the next start asks for it. */
export async function forgetVaultKey(): Promise<VaultStatus> {
  return parse(
    await apiFetch(VAULT_FORGET_PATH, { method: "POST" }),
    vaultStatusSchema,
    "The vault response is malformed.",
  );
}

/** Removes the sealed copy. The configuration keeps its plaintext values. */
export async function destroyVault(): Promise<VaultStatus> {
  return parse(
    await apiFetch(VAULT_PATH, { method: "DELETE" }),
    vaultStatusSchema,
    "The vault response is malformed.",
  );
}

export function vaultQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.vault,
    queryFn: fetchVaultStatus,
  });
}
