import { Button, Chip, Label, Spinner, TextArea, TextField } from "@heroui/react";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import TrashIcon from "~icons/gravity-ui/trash-bin";
import { $api } from "@/api/client";
import { userMessage } from "@/api/errors";
import { ConfirmDialog } from "@/components/dialogs/confirm-dialog";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { newIdempotencyKey } from "@/features/shared/idempotency";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/bots")({
  component: BotsSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

function BotsSettings() {
  const [token, setToken] = useState("");
  const [isAddingBots, setIsAddingBots] = useState(false);
  const [deleteBot, setDeleteBot] = useState<{ id: number; name: string } | null>(null);
  const query = $api.useSuspenseQuery(
    "get",
    "/v1/bots",
    { params: { query: { limit: 200 } } },
    { staleTime: 20_000 },
  );
  const create = $api.useMutation("post", "/v1/bots");
  const remove = $api.useMutation("delete", "/v1/bots/{botId}", {
    onSuccess: () => {
      setDeleteBot(null);
      void refresh();
      toast.success(t("Telegram bot deleted"));
    },
    onError: (error) => {
      toast.error(t("Telegram bot could not be deleted"), { description: userMessage(error) });
    },
  });
  const refresh = () =>
    getQueryClient().invalidateQueries({ queryKey: $api.queryOptions("get", "/v1/bots").queryKey });

  const handleAddBots = async () => {
    const tokens = [
      ...new Set(
        token
          .split(/\r?\n/)
          .map((value) => value.trim())
          .filter(Boolean),
      ),
    ];
    if (tokens.length === 0 || isAddingBots) return;
    setIsAddingBots(true);

    try {
      const result = await create.mutateAsync({
        params: { header: { "Idempotency-Key": newIdempotencyKey() } },
        body: { tokens },
      });
      const failed = new Set(result.failedIndexes);
      setToken(tokens.filter((_, index) => failed.has(index)).join("\n"));
      await refresh();
      if (result.bots.length > 0) {
        toast.success(
          `${result.bots.length} bot${result.bots.length === 1 ? "" : "s"} queued for verification`,
        );
      }
      if (result.failedIndexes.length > 0) {
        toast.warning(
          `${result.failedIndexes.length} token${result.failedIndexes.length === 1 ? "" : "s"} had an invalid format`,
        );
      }
    } catch (error) {
      toast.error(t("Bots could not be queued"), { description: userMessage(error) });
    } finally {
      setIsAddingBots(false);
    }
  };

  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("Telegram bots")}
        description={t("Bot accounts used for parallel Telegram API throughput.")}
      />
      <SettingsSection
        title={t("Add bots")}
        description={t("Paste one BotFather token per line. Bots are stored immediately; existing channels are updated in the background.")}
      >
        <SettingsRow
          label={t("Bot tokens")}
          description={t("Tokens are sent only to your Teldrive server and are never shown again.")}
        >
          <div className="flex flex-col items-stretch gap-2 sm:flex-row sm:items-end">
            <TextField className="min-w-0 flex-1">
              <Label className="sr-only">{t("Bot tokens")}</Label>
              <TextArea
                value={token}
                onChange={(event) => setToken(event.currentTarget.value)}
                placeholder={"123456:ABC...\n789012:DEF..."}
                rows={5}
                className="h-32 min-h-32 max-h-32 resize-none overflow-y-auto font-mono"
              />
            </TextField>
            <Button
              className="shrink-0"
              onPress={handleAddBots}
              isDisabled={!token.trim() || isAddingBots}
              isPending={isAddingBots}
            >
              {t("Add bots")}
            </Button>
          </div>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection
        title={t("Configured bots")}
        description={t("Healthy enabled bots are used automatically by the storage runtime.")}
      >
        {query.data.items.length ? (
          query.data.items.map((bot) => (
            <SettingsRow
              key={bot.id}
              label={`@${bot.username || `bot-${bot.id}`}`}
              description={t("Added {{value0}}", { value0: new Date(bot.createdAt).toLocaleString() })}
            >
              <div className="flex items-center justify-end gap-2">
                <Chip color={bot.enabled ? "success" : "warning"} variant="tertiary">
                  {bot.enabled ? t("Enabled") : t("Disabled")}
                </Chip>
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  aria-label={`Delete bot ${bot.username || bot.id}`}
                  isDisabled={remove.isPending && deleteBot?.id === bot.id}
                  onPress={() =>
                    setDeleteBot({ id: bot.id, name: bot.username || `bot-${bot.id}` })
                  }
                >
                  <TrashIcon className="size-4" />
                </Button>
              </div>
            </SettingsRow>
          ))
        ) : (
          <div className="px-5 py-8 text-sm text-muted">{t("No Telegram bots are configured.")}</div>
        )}
      </SettingsSection>
      <ConfirmDialog
        open={deleteBot !== null}
        onOpenChange={(open) => {
          if (!open && !remove.isPending) setDeleteBot(null);
        }}
        title={t("Delete Telegram bot?")}
        message={t("Uploads using other bots or your user session will continue after “{{value0}}” is removed.", { value0: deleteBot?.name ?? "" })}
        confirmLabel={t("Delete bot")}
        isPending={remove.isPending}
        onConfirm={() => {
          if (deleteBot) {
            remove.mutate({ params: { path: { botId: deleteBot.id } } });
          }
        }}
      />
    </div>
  );
}
