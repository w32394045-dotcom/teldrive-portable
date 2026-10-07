import { Button, Chip, Input, Spinner, TextField, Label } from "@heroui/react";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import CheckIcon from "~icons/gravity-ui/check";
import RefreshIcon from "~icons/gravity-ui/arrow-rotate-left";
import TrashIcon from "~icons/gravity-ui/trash-bin";
import { $api } from "@/api/client";
import { userMessage } from "@/api/errors";
import { ConfirmDialog } from "@/components/dialogs/confirm-dialog";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { newIdempotencyKey } from "@/features/shared/idempotency";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/channels")({
  component: ChannelsSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

function ChannelsSettings() {
  const [name, setName] = useState("");
  const [deleteChannel, setDeleteChannel] = useState<{ id: number; name: string } | null>(null);
  const query = $api.useSuspenseQuery(
    "get",
    "/v1/channels",
    { params: { query: { limit: 200 } } },
    { staleTime: 20_000 },
  );
  const create = $api.useMutation("post", "/v1/channels");
  const select = $api.useMutation("post", "/v1/channels/{channelId}/select");
  const sync = $api.useMutation("post", "/v1/channels/sync");
  const remove = $api.useMutation("delete", "/v1/channels/{channelId}", {
    onSuccess: () => {
      setDeleteChannel(null);
      void refresh();
      toast.success(t("Storage channel deleted"));
    },
    onError: (error) => {
      toast.error(t("Storage channel could not be deleted"), { description: userMessage(error) });
    },
  });
  const refresh = () =>
    getQueryClient().invalidateQueries({
      queryKey: $api.queryOptions("get", "/v1/channels").queryKey,
    });

  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("Storage channels")}
        description={t("Telegram channels used to store encrypted file parts.")}
        actions={
          <Button
            variant="secondary"
            onPress={async () => {
              try {
                await sync.mutateAsync({
                  params: { header: { "Idempotency-Key": newIdempotencyKey() } },
                });
                await refresh();
                toast.success(t("Channels synchronized"));
              } catch (error) {
                toast.error(t("Channel sync failed"), { description: userMessage(error) });
              }
            }}
            isDisabled={sync.isPending}
          >
            <RefreshIcon className="size-4" />
            {t("Discover and sync")}
          </Button>
        }
      />
      <SettingsSection
        title={t("Create channel")}
        description={t("Teldrive will create and configure a Telegram storage channel.")}
      >
        <SettingsRow
          label={t("Channel name")}
          description={t("Use a recognizable name for this storage target.")}
        >
          <div className="flex gap-2">
            <TextField className="min-w-0 flex-1">
              <Label className="sr-only">{t("Channel name")}</Label>
              <Input
                value={name}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder={t("Teldrive Storage")}
              />
            </TextField>
            <Button
              onPress={async () => {
                if (!name.trim()) return;
                try {
                  await create.mutateAsync({
                    params: { header: { "Idempotency-Key": newIdempotencyKey() } },
                    body: { name: name.trim(), selected: false },
                  });
                  setName("");
                  await refresh();
                  toast.success(t("Storage channel created"));
                } catch (error) {
                  toast.error(t("Channel could not be created"), { description: userMessage(error) });
                }
              }}
              isDisabled={!name.trim() || create.isPending}
            >
              {t("Create")}
            </Button>
          </div>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection
        title={t("Configured channels")}
        description={t("Choose the active channel or remove unused empty channels.")}
      >
        {query.data.items.length ? (
          query.data.items.map((channel) => (
            <SettingsRow
              key={channel.id}
              label={channel.name}
              description={t("Channel {{id}}", { id: channel.id })}
            >
              <div className="flex items-center justify-end gap-2">
                {channel.selected ? (
                  <Chip color="success" variant="tertiary">
                    <CheckIcon className="size-3" />
                    {t("Selected")}
                  </Chip>
                ) : (
                  <Button
                    size="sm"
                    variant="secondary"
                    onPress={async () => {
                      await select.mutateAsync({ params: { path: { channelId: channel.id } } });
                      await refresh();
                    }}
                  >
                    {t("Use channel")}
                  </Button>
                )}
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  aria-label={t("Delete {{name}}", { name: channel.name })}
                  isDisabled={remove.isPending && deleteChannel?.id === channel.id}
                  onPress={() => setDeleteChannel({ id: channel.id, name: channel.name })}
                >
                  <TrashIcon className="size-4" />
                </Button>
              </div>
            </SettingsRow>
          ))
        ) : (
          <div className="px-5 py-8 text-sm text-muted">{t("No storage channels are configured.")}</div>
        )}
      </SettingsSection>
      <ConfirmDialog
        open={deleteChannel !== null}
        onOpenChange={(open) => {
          if (!open && !remove.isPending) setDeleteChannel(null);
        }}
        title={t("Delete storage channel?")}
        message={`“${deleteChannel?.name ?? ""}” can only be deleted when no files reference it.`}
        confirmLabel="Delete channel"
        isPending={remove.isPending}
        onConfirm={() => {
          if (deleteChannel) {
            remove.mutate({ params: { path: { channelId: deleteChannel.id } } });
          }
        }}
      />
    </div>
  );
}
