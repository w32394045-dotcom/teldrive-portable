import { Button, Input, Label, Spinner, TextField } from "@heroui/react";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import CopyIcon from "~icons/gravity-ui/copy";
import TrashIcon from "~icons/gravity-ui/trash-bin";
import { $api } from "@/api/client";
import { userMessage } from "@/api/errors";
import type { ApiKeyCreated } from "@/api/types";
import { ConfirmDialog } from "@/components/dialogs/confirm-dialog";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { newIdempotencyKey } from "@/features/shared/idempotency";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/api-keys")({
  component: ApiKeysSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

function formatDate(value?: string | null) {
  return value ? new Date(value).toLocaleString() : "never";
}

function ApiKeysSettings() {
  const [name, setName] = useState("");
  const [created, setCreated] = useState<ApiKeyCreated>();
  const [revokeKey, setRevokeKey] = useState<{ id: string; name: string } | null>(null);
  const query = $api.useSuspenseQuery(
    "get",
    "/v1/api-keys",
    { params: { query: { limit: 200 } } },
    { staleTime: 20_000 },
  );
  const create = $api.useMutation("post", "/v1/api-keys");
  const revoke = $api.useMutation("delete", "/v1/api-keys/{apiKeyId}", {
    onSuccess: () => {
      setRevokeKey(null);
      void refresh();
      toast.success(t("API key revoked"));
    },
    onError: (error) => {
      toast.error(t("API key could not be revoked"), { description: userMessage(error) });
    },
  });
  const refresh = () =>
    getQueryClient().invalidateQueries({
      queryKey: $api.queryOptions("get", "/v1/api-keys").queryKey,
    });

  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("API keys")}
        description={t("Credentials for rclone, external API clients, and signing in to a published instance. The secret is shown once.")}
      />
      <SettingsSection
        title={t("Create API key")}
        description={t("The secret is shown once. Store it in a password manager.")}
      >
        <SettingsRow
          label={t("Key name")}
          description={t("Use a name that identifies the client or machine.")}
        >
          <div className="flex gap-2">
            <TextField className="min-w-0 flex-1">
              <Label className="sr-only">{t("Key name")}</Label>
              <Input
                value={name}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder={t("rclone laptop")}
              />
            </TextField>
            <Button
              onPress={async () => {
                if (!name.trim()) return;
                try {
                  const result = await create.mutateAsync({
                    params: { header: { "Idempotency-Key": newIdempotencyKey() } },
                    body: { name: name.trim() },
                  });
                  setCreated(result);
                  setName("");
                  await refresh();
                } catch (error) {
                  toast.error(t("API key could not be created"), { description: userMessage(error) });
                }
              }}
              isDisabled={!name.trim() || create.isPending}
            >
              {t("Create")}
            </Button>
          </div>
        </SettingsRow>
        {created ? (
          <SettingsRow
            label={t("New API key secret")}
            description={t("Copy this value now. It cannot be retrieved later.")}
          >
            <div className="flex gap-2">
              <Input readOnly value={created.secret} className="min-w-0 flex-1 font-mono" />
              <Button
                isIconOnly
                variant="secondary"
                aria-label={t("Copy API key")}
                onPress={() => {
                  void navigator.clipboard.writeText(created.secret);
                  toast.success(t("API key copied"));
                }}
              >
                <CopyIcon className="size-4" />
              </Button>
            </div>
          </SettingsRow>
        ) : null}
      </SettingsSection>
      <SettingsSection
        title={t("Existing API keys")}
        description={t("Revoke credentials that are no longer in use.")}
      >
        {query.data.items.length ? (
          query.data.items.map((item) => (
            <SettingsRow
              key={item.id}
              label={item.name}
              description={t("Created {{value0}} · last used {{value1}}", { value0: formatDate(item.createdAt), value1: formatDate(item.lastUsedAt) })}
            >
              <div className="flex justify-end">
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  aria-label={t("Revoke {{name}}", { name: item.name })}
                  isDisabled={revoke.isPending && revokeKey?.id === item.id}
                  onPress={() => setRevokeKey({ id: item.id, name: item.name })}
                >
                  <TrashIcon className="size-4" />
                </Button>
              </div>
            </SettingsRow>
          ))
        ) : (
          <div className="px-5 py-8 text-sm text-muted">{t("No API keys created.")}</div>
        )}
      </SettingsSection>
      <ConfirmDialog
        open={revokeKey !== null}
        onOpenChange={(open) => {
          if (!open && !revoke.isPending) setRevokeKey(null);
        }}
        title={t("Revoke API key?")}
        message={t("Applications using “{{value0}}” will lose access immediately.", { value0: revokeKey?.name ?? "" })}
        confirmLabel={t("Revoke key")}
        isPending={revoke.isPending}
        onConfirm={() => {
          if (revokeKey) {
            revoke.mutate({ params: { path: { apiKeyId: revokeKey.id } } });
          }
        }}
      />
    </div>
  );
}
