import { Button, Chip, Input, Label, Spinner, Switch, Typography } from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { toast } from "sonner";
import CopyIcon from "~icons/gravity-ui/copy";
import { userMessage } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { copyText } from "@/features/files/download";
import { updateWebDAVConfig, webdavConfigQueryOptions } from "@/features/webdav/config";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/webdav")({
  component: WebDAVSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

function WebDAVSettings() {
  const config = useQuery(webdavConfigQueryOptions());
  const update = useMutation({
    mutationFn: updateWebDAVConfig,
    onSuccess: (saved) => {
      // The response is the authoritative state, so the switch can never claim a
      // value the server did not store.
      getQueryClient().setQueryData(queryKeys.webdavConfig, saved);
      toast.success(t("WebDAV access updated"));
    },
    onError: (error) => {
      toast.error(t("WebDAV settings could not be saved"), { description: userMessage(error) });
    },
  });

  const enabled = config.data?.enabled ?? false;
  const busy = config.isPending || update.isPending;
  const connectionUrl = config.data?.url ?? "";

  const copyConnectionUrl = async () => {
    try {
      await copyText(connectionUrl);
      toast.success(t("Connection URL copied"));
    } catch {
      // The clipboard is unavailable here (insecure context or a denied
      // permission); the address is still readable and selectable above.
      toast.error(t("Connection URL could not be copied"));
    }
  };

  return (
    <div className="space-y-6">
      <SettingsPageHeader
        title={t("WebDAV")}
        description={t("Mount your Teldrive files in a file manager or rclone over WebDAV.")}
      />
      <SettingsSection
        title={t("WebDAV access")}
        description={t(
          "While WebDAV is on, this server accepts DAV clients that sign in with an API key created under Settings → API keys.",
        )}
      >
        <SettingsRow
          label={t("Enable WebDAV")}
          description={t(
            "Give file managers and rclone read and write access to your drive through this server.",
          )}
        >
          <div className="flex items-center justify-end gap-3">
            {config.data ? (
              <Chip color={enabled ? "success" : "warning"} variant="tertiary">
                {enabled ? t("Enabled") : t("Disabled")}
              </Chip>
            ) : null}
            {busy ? <Spinner size="sm" /> : null}
            <Switch
              aria-label={t("Enable WebDAV")}
              isSelected={enabled}
              isDisabled={busy || config.isError}
              onChange={(isSelected) => update.mutate(isSelected)}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Label className="sr-only">{t("Enable WebDAV")}</Label>
              </Switch.Content>
            </Switch>
          </div>
        </SettingsRow>
        {config.isError ? (
          <SettingsRow
            label={t("WebDAV settings could not be loaded")}
            description={userMessage(config.error)}
          >
            <div className="flex justify-end">
              <Button variant="secondary" onPress={() => void config.refetch()}>
                {t("Retry")}
              </Button>
            </div>
          </SettingsRow>
        ) : null}
      </SettingsSection>
      <SettingsSection
        title={t("Client setup")}
        description={t(
          "Paste this address into your DAV client. The password is an API key created under Settings → API keys, not your Teldrive password.",
        )}
      >
        <SettingsRow
          label={t("Connection URL")}
          description={t("Read-only address that this Teldrive server exposes for DAV clients.")}
        >
          <div className="flex gap-2">
            <Input
              readOnly
              aria-label={t("Connection URL")}
              value={connectionUrl}
              className="min-w-0 flex-1 font-mono"
            />
            <Button
              isIconOnly
              variant="secondary"
              aria-label={t("Copy WebDAV URL")}
              isDisabled={!connectionUrl}
              onPress={() => void copyConnectionUrl()}
            >
              <CopyIcon className="size-4" />
            </Button>
          </div>
        </SettingsRow>
        <SettingsRow
          label={t("Client hints")}
          description={t(
            "Every client connects over HTTP Basic authentication: the user name can be anything, and the password must be an API key.",
          )}
          align="start"
        >
          <div className="flex flex-col gap-4">
            <ClientHint
              name={t("Windows")}
              hint={t(
                "In File Explorer, choose “Map network drive” (映射网络驱动器), enter the connection URL above, and use an API key as the password.",
              )}
            />
            <ClientHint
              name={t("macOS")}
              hint={t(
                "In Finder, choose Go → Connect to Server (前往 → 连接服务器), enter the connection URL, and use an API key as the password.",
              )}
            />
            <ClientHint
              name={t("rclone")}
              hint={t(
                "Create a WebDAV remote with the command below, and use an API key as the password.",
              )}
            >
              {connectionUrl ? (
                <pre className="mt-2 overflow-x-auto rounded-lg bg-muted/15 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
                  {t(
                    "rclone config create teldrive webdav url {{url}} vendor other user teldrive pass <API key>",
                    { url: connectionUrl },
                  )}
                </pre>
              ) : config.isPending ? (
                <Typography.Paragraph className="mt-2 text-xs text-muted">
                  {t("Reading settings")}
                </Typography.Paragraph>
              ) : null}
            </ClientHint>
          </div>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection
        title={t("What works today")}
        description={t(
          "Browsing, downloading, uploading, creating folders, renaming and deleting are supported over WebDAV.",
        )}
      >
        <SettingsRow
          label={t("Deleting")}
          description={t(
            "Deleting an item moves it to the trash, where it can be restored. WebDAV never purges files permanently.",
          )}
        >
          <div className="flex justify-end">
            <Chip color="success" variant="tertiary">
              {t("Supported")}
            </Chip>
          </div>
        </SettingsRow>
        <SettingsRow
          label={t("File locking")}
          description={t(
            "Locking is not supported, so a client that requires it may refuse to edit files in place.",
          )}
        >
          <div className="flex justify-end">
            <Chip color="warning" variant="tertiary">
              {t("Not supported")}
            </Chip>
          </div>
        </SettingsRow>
      </SettingsSection>
    </div>
  );
}

function ClientHint({
  name,
  hint,
  children,
}: {
  name: string;
  hint: string;
  children?: ReactNode;
}) {
  return (
    <div>
      <p className="text-xs font-semibold text-foreground">{name}</p>
      <Typography.Paragraph className="mt-1 text-xs leading-relaxed text-muted">
        {hint}
      </Typography.Paragraph>
      {children}
    </div>
  );
}
