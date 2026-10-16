import { Button, Chip, Label, Spinner, Switch, Typography } from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { toast } from "sonner";
import { userMessage } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { copyText } from "@/features/files/download";
import {
  decideDevice,
  type LanDevice,
  lanQueryOptions,
  setNetworkEnabled,
  systemStatusQueryOptions,
} from "@/features/lan/config";
import { t } from "@/i18n";
import { getQueryClient } from "@/lib/queryClient";

export const Route = createFileRoute("/_settings/settings/lan")({
  component: LanSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

function LanSettings() {
  const status = useQuery(lanQueryOptions());
  const where = useQuery(systemStatusQueryOptions());

  const networkUrl = where.data?.networkUrl ?? "";
  const listening = where.data?.boundToNetwork ?? false;

  const copyAddress = async () => {
    try {
      await copyText(networkUrl);
      toast.success(t("Address copied"));
    } catch {
      // An insecure context or a denied permission; the address is still on
      // screen and can be selected by hand.
      toast.error(t("The address could not be copied"));
    }
  };

  const onSaved = (saved: Awaited<ReturnType<typeof decideDevice>>) => {
    getQueryClient().setQueryData(queryKeys.lanAccess, saved);
  };

  const network = useMutation({
    mutationFn: setNetworkEnabled,
    onSuccess: (saved) => {
      onSaved(saved);
      toast.success(
        saved.restartRequired
          ? t("Saved. Restart the service for it to take effect.")
          : t("Network access updated"),
      );
    },
    onError: (error) => {
      toast.error(t("Network access could not be changed"), { description: userMessage(error) });
    },
  });

  const decide = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "approve" | "deny" | "forget" }) =>
      decideDevice(id, action),
    onSuccess: (_saved, variables) => {
      onSaved(_saved);
      toast.success(
        variables.action === "approve"
          ? t("Device approved")
          : variables.action === "deny"
            ? t("Device refused")
            : t("Device forgotten"),
      );
    },
    onError: (error) => {
      toast.error(t("The decision could not be saved"), { description: userMessage(error) });
    },
  });

  const current = status.data;
  const enabled = current?.networkEnabled ?? false;
  const busy = status.isPending || network.isPending || decide.isPending;
  const pending = current?.pending ?? [];
  const decided = (current?.devices ?? []).filter((device) => device.state !== "pending");

  return (
    <>
      <SettingsPageHeader
        title={t("Local network access")}
        description={t(
          "By default this drive is reachable only from this computer. Turning network access on lets other devices on the same network ask for permission, and nothing works until you approve them one by one.",
        )}
      />

      <SettingsSection
        title={t("Reachability")}
        description={t(
          "Approved devices borrow this computer's session, so they see the drive exactly as it is here. They do not sign in to Telegram themselves.",
        )}
      >
        <SettingsRow
          label={t("Reachable on the local network")}
          description={t(
            "Takes effect at the next start, because a listening socket cannot be widened while it is in use.",
          )}
        >
          <div className="flex items-center justify-end gap-3">
            {status.isPending ? <Spinner size="sm" /> : null}
            {current?.restartRequired ? (
              <Chip color="warning" variant="tertiary">
                {t("Restart needed")}
              </Chip>
            ) : null}
            <Switch
              aria-label={t("Reachable on the local network")}
              isSelected={enabled}
              isDisabled={busy}
              onChange={(isSelected) => network.mutate(isSelected)}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Label className="sr-only">{t("Reachable on the local network")}</Label>
              </Switch.Content>
            </Switch>
          </div>
        </SettingsRow>
        {enabled ? (
          <SettingsRow label={t("Warning")} align="start">
            <Typography.Paragraph className="text-xs leading-relaxed text-warning">
              {t(
                "While this is on, any device on the same network can reach the port. They still cannot use the drive without approval, but they can see that it is there.",
              )}
            </Typography.Paragraph>
          </SettingsRow>
        ) : null}
      </SettingsSection>

      <SettingsSection
        title={t("Where to open it")}
        description={t(
          "Type this address on the other device. It works only while this computer is running.",
        )}
      >
        {networkUrl ? (
          <SettingsRow label={t("Address for other devices")} description={networkUrl}>
            <div className="flex items-center justify-end gap-3">
              {listening ? (
                <Chip color="success" variant="tertiary">
                  {t("Listening")}
                </Chip>
              ) : (
                <Chip color="warning" variant="tertiary">
                  {t("Restart needed")}
                </Chip>
              )}
              <Button variant="tertiary" onPress={() => void copyAddress()}>
                {t("Copy")}
              </Button>
            </div>
          </SettingsRow>
        ) : (
          <SettingsRow label={t("No network address found")}>
            <div className="flex justify-end">
              <Typography.Paragraph className="max-w-md text-right text-xs text-muted">
                {t(
                  "This computer has no address another device could reach. Connect it to a network and reload this page.",
                )}
              </Typography.Paragraph>
            </div>
          </SettingsRow>
        )}
      </SettingsSection>

      <SettingsSection
        title={t("Waiting for you")}
        description={t("A request nobody answers is forgotten after ten minutes.")}
      >
        {pending.length === 0 ? (
          <SettingsRow label={t("Nothing is waiting")}>
            <div className="flex justify-end">
              <Typography.Paragraph className="text-xs text-muted">
                {t("Requests from other devices appear here.")}
              </Typography.Paragraph>
            </div>
          </SettingsRow>
        ) : (
          pending.map((device) => (
            <SettingsRow
              key={device.id}
              label={device.name}
              description={
                device.address
                  ? `${device.address} · ${formatMoment(device.createdAt)}`
                  : formatMoment(device.createdAt)
              }
            >
              <div className="flex items-center justify-end gap-3">
                <Button
                  variant="tertiary"
                  isDisabled={busy}
                  onPress={() => decide.mutate({ id: device.id, action: "deny" })}
                >
                  {t("Refuse")}
                </Button>
                <Button
                  variant="secondary"
                  isDisabled={busy}
                  onPress={() => decide.mutate({ id: device.id, action: "approve" })}
                >
                  {t("Approve")}
                </Button>
              </div>
            </SettingsRow>
          ))
        )}
      </SettingsSection>

      <SettingsSection
        title={t("Decided")}
        description={t(
          "Revoking ends access immediately. Forgetting removes the record, which is how a device is allowed to ask again.",
        )}
      >
        {decided.length === 0 ? (
          <SettingsRow label={t("No devices yet")}>
            <div className="flex justify-end">
              <Typography.Paragraph className="text-xs text-muted">
                {t("Approved and refused devices are listed here.")}
              </Typography.Paragraph>
            </div>
          </SettingsRow>
        ) : (
          decided.map((device) => (
            <SettingsRow key={device.id} label={device.name} description={describeDevice(device)}>
              <div className="flex items-center justify-end gap-3">
                {device.state === "approved" ? (
                  <Chip color="success" variant="tertiary">
                    {t("Approved")}
                  </Chip>
                ) : (
                  <Chip color="warning" variant="tertiary">
                    {t("Refused")}
                  </Chip>
                )}
                {device.state === "approved" ? (
                  <Button
                    variant="tertiary"
                    isDisabled={busy}
                    onPress={() => decide.mutate({ id: device.id, action: "deny" })}
                  >
                    {t("Revoke")}
                  </Button>
                ) : null}
                <Button
                  variant="tertiary"
                  isDisabled={busy}
                  onPress={() => decide.mutate({ id: device.id, action: "forget" })}
                >
                  {t("Forget")}
                </Button>
              </div>
            </SettingsRow>
          ))
        )}
      </SettingsSection>
    </>
  );
}

function describeDevice(device: LanDevice): string {
  const parts: string[] = [];
  if (device.address) parts.push(device.address);
  if (device.decidedAt) parts.push(formatMoment(device.decidedAt));
  if (device.lastSeenAt)
    parts.push(t("Last used {{time}}", { time: formatMoment(device.lastSeenAt) }));
  return parts.join(" · ");
}

function formatMoment(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}
