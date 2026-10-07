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
import {
  autostartQueryOptions,
  createMount,
  deleteMount,
  mountQueryOptions,
  updateAutostart,
} from "@/features/system/config";
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
      <AutomationSection enabled={enabled} />
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

/**
 * Descriptions the server reports for the Windows redirector prerequisites, keyed
 * so the wording stays translatable instead of shipping one language from Go.
 */
const PREREQUISITE_LABEL: Record<string, string> = {
  webclient_service: "The WebClient (WebDAV redirector) service must start automatically.",
  basic_auth_level:
    "Windows only allows Basic authentication over HTTPS by default; this server is http, so BasicAuthLevel must be 2.",
  file_size_limit:
    "Windows caps WebDAV downloads at 50 MB by default; raise FileSizeLimitInBytes for larger files.",
  platform: "This feature is only implemented for Windows.",
};

/**
 * Starts Teldrive at login and maps the DAV tree to a drive letter. The two are
 * deliberately presented together: a mapping only survives a reboot if the server
 * comes back with it, so the section tells the user when one is missing.
 */
function AutomationSection({ enabled }: { enabled: boolean }) {
  const autostart = useQuery(autostartQueryOptions());
  const mount = useQuery(mountQueryOptions());

  const autostartMutation = useMutation({
    mutationFn: updateAutostart,
    onSuccess: (saved) => {
      getQueryClient().setQueryData(queryKeys.autostart, saved);
      toast.success(t("Start at login updated"));
    },
    onError: (error) => {
      toast.error(t("The start-at-login setting could not be saved"), {
        description: userMessage(error),
      });
    },
  });

  const mountMutation = useMutation({
    mutationFn: (elevate: boolean) => createMount(elevate),
    onSuccess: (saved) => {
      getQueryClient().setQueryData(queryKeys.webdavMount, saved);
      if (saved.mounted) toast.success(t("Drive mounted"));
    },
    onError: (error) => {
      toast.error(t("The drive could not be mounted"), { description: userMessage(error) });
    },
  });

  const unmountMutation = useMutation({
    mutationFn: deleteMount,
    onSuccess: (saved) => {
      getQueryClient().setQueryData(queryKeys.webdavMount, saved);
      toast.success(t("Drive unmounted"));
    },
    onError: (error) => {
      toast.error(t("The drive could not be unmounted"), { description: userMessage(error) });
    },
  });

  const autostartOn = autostart.data?.enabled ?? false;
  const mounted = mount.data?.mounted ?? false;
  const drive = mount.data?.drive ?? "";
  const prerequisites = mount.data?.prerequisites;
  const busy =
    autostart.isPending ||
    mount.isPending ||
    autostartMutation.isPending ||
    mountMutation.isPending ||
    unmountMutation.isPending;

  return (
    <SettingsSection
      title={t("Keep it running")}
      description={t(
        "Start Teldrive at login and mount the drive automatically, so your files are available every time you sign in.",
      )}
    >
      {!enabled ? (
        <SettingsRow
          label={t("WebDAV is off")}
          description={t("Turn WebDAV on above before starting it at login or mounting a drive.")}
        >
          <div className="flex justify-end">
            <Chip color="warning" variant="tertiary">
              {t("Disabled")}
            </Chip>
          </div>
        </SettingsRow>
      ) : null}
      <SettingsRow
        label={t("Start at login")}
        description={t(
          "Launch Teldrive quietly when you sign in to Windows, so the mapped drive is always available.",
        )}
      >
        <div className="flex items-center justify-end gap-3">
          {autostart.data ? (
            <Chip color={autostartOn ? "success" : "warning"} variant="tertiary">
              {autostartOn ? t("Enabled") : t("Disabled")}
            </Chip>
          ) : null}
          {busy ? <Spinner size="sm" /> : null}
          <Switch
            aria-label={t("Start at login")}
            isSelected={autostartOn}
            isDisabled={busy || autostart.isError || autostart.data?.supported === false}
            onChange={(isSelected) => autostartMutation.mutate(isSelected)}
          >
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Label className="sr-only">{t("Start at login")}</Label>
            </Switch.Content>
          </Switch>
        </div>
      </SettingsRow>
      <SettingsRow
        label={t("Mount as a drive")}
        description={t(
          "Map the WebDAV address above to a drive letter and keep it after a reboot.",
        )}
        align="start"
      >
        <div className="flex flex-col items-end gap-3">
          <div className="flex items-center gap-3">
            {mounted ? (
              <Chip color="success" variant="tertiary">
                {t("Mounted at {{drive}}", { drive })}
              </Chip>
            ) : mount.data ? (
              <Chip color="warning" variant="tertiary">
                {t("Not mounted")}
              </Chip>
            ) : null}
            {mounted ? (
              <Button
                variant="secondary"
                isDisabled={busy}
                onPress={() => unmountMutation.mutate()}
              >
                {t("Unmount")}
              </Button>
            ) : (
              <Button
                variant="secondary"
                isDisabled={busy || !enabled || mount.isError}
                onPress={() => mountMutation.mutate(false)}
              >
                {t("Mount")}
              </Button>
            )}
          </div>
          {prerequisites && !prerequisites.ready ? (
            <div className="w-full space-y-2 rounded-lg bg-muted/15 p-3 text-left">
              <p className="text-xs font-semibold text-foreground">
                {t("Windows needs a small configuration change before a drive can be mapped")}
              </p>
              <ul className="space-y-1">
                {prerequisites.items
                  .filter((item) => !item.ok)
                  .map((item) => (
                    <li key={item.key} className="text-xs leading-relaxed text-muted">
                      • {t(PREREQUISITE_LABEL[item.key] ?? item.description)}
                      {item.current || item.required ? (
                        <span className="text-muted/80">
                          {" "}
                          {t("Current")}: {item.current ?? "—"} → {t("Needed")}: {item.required ?? "—"}
                        </span>
                      ) : null}
                    </li>
                  ))}
              </ul>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="secondary"
                  isDisabled={busy}
                  onPress={() => mountMutation.mutate(true)}
                >
                  {t("Fix with administrator rights")}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onPress={() => void copyFixCommand(prerequisites.fixCommand)}
                >
                  {t("Copy the command")}
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      </SettingsRow>
      {mount.data && autostart.data ? (
        <SettingsRow
          label={t("Keeping the drive mounted")}
          description={
            mounted && !autostartOn
              ? t(
                  "The drive is mounted, but Teldrive does not start at login — after a reboot you would have to start it yourself, and the drive would disappear.",
                )
              : mounted && autostartOn
                ? t("Teldrive starts at login and the drive is mounted automatically.")
                : autostartOn
                  ? t("Teldrive starts at login; mount the drive to make it available straight away.")
                  : t("Enable both to have the drive ready after every sign-in.")
          }
          align="start"
        >
          <div className="flex justify-end gap-2">
            {mounted && !autostartOn ? (
              <Button
                size="sm"
                variant="secondary"
                isDisabled={busy}
                onPress={() => autostartMutation.mutate(true)}
              >
                {t("Enable start at login")}
              </Button>
            ) : null}
            {autostartOn && !mounted ? (
              <Button
                size="sm"
                variant="secondary"
                isDisabled={busy || !enabled}
                onPress={() => mountMutation.mutate(false)}
              >
                {t("Mount")}
              </Button>
            ) : null}
          </div>
        </SettingsRow>
      ) : null}
    </SettingsSection>
  );
}

async function copyFixCommand(command: string) {
  try {
    await copyText(command);
    toast.success(t("Command copied"));
  } catch {
    toast.error(t("The command could not be copied"));
  }
}
