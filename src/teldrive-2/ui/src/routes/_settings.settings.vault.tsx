import {
  Button,
  Chip,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextField,
  Typography,
} from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import CopyIcon from "~icons/gravity-ui/copy";
import { userMessage } from "@/api/errors";
import { queryKeys } from "@/api/query-keys";
import { SettingsPageHeader, SettingsRow, SettingsSection } from "@/components/settings-layout";
import { copyText } from "@/features/files/download";
import {
  createVault,
  destroyVault,
  forgetVaultKey,
  type VaultKeyMode,
  vaultQueryOptions,
} from "@/features/vault/config";
import { getQueryClient } from "@/lib/queryClient";
import { t } from "@/i18n";

export const Route = createFileRoute("/_settings/settings/vault")({
  component: VaultSettings,
  pendingComponent: () => (
    <div className="flex justify-center py-16">
      <Spinner size="lg" />
    </div>
  ),
});

const MODE_LABELS: Record<VaultKeyMode, string> = {
  saved: "Remember the key on this machine",
  prompt: "Ask for the key at every start",
};

function VaultSettings() {
  const status = useQuery(vaultQueryOptions());
  const [key, setKey] = useState("");
  const [mode, setMode] = useState<VaultKeyMode>("saved");
  const [issuedKey, setIssuedKey] = useState("");
  const [confirmDestroy, setConfirmDestroy] = useState(false);

  const create = useMutation({
    mutationFn: () => createVault(key, mode),
    onSuccess: (created) => {
      getQueryClient().setQueryData(queryKeys.vault, created.status);
      setKey("");
      // A generated key is shown exactly once, so it gets a panel of its own
      // rather than a toast that disappears.
      setIssuedKey(created.key ?? "");
      toast.success(t("The secrets are now sealed"));
    },
    onError: (error) => {
      toast.error(t("The vault could not be sealed"), { description: userMessage(error) });
    },
  });

  const forget = useMutation({
    mutationFn: forgetVaultKey,
    onSuccess: (saved) => {
      getQueryClient().setQueryData(queryKeys.vault, saved);
      toast.success(t("The key will be asked for at the next start"));
    },
    onError: (error) => {
      toast.error(t("The remembered key could not be removed"), {
        description: userMessage(error),
      });
    },
  });

  const destroy = useMutation({
    mutationFn: destroyVault,
    onSuccess: (saved) => {
      getQueryClient().setQueryData(queryKeys.vault, saved);
      setConfirmDestroy(false);
      setIssuedKey("");
      toast.success(t("The sealed copy was removed"));
    },
    onError: (error) => {
      toast.error(t("The vault could not be removed"), { description: userMessage(error) });
    },
  });

  const current = status.data;
  const busy = status.isPending || create.isPending || forget.isPending || destroy.isPending;
  const sealed = current?.exists ?? false;
  const tooShort = key.length > 0 && key.length < (current?.minimumKeyLength ?? 8);

  return (
    <>
      <SettingsPageHeader
        title={t("Settings vault")}
        description={t(
          "The keys that decrypt your data and authenticate every session live in config.toml, in the clear. Sealing them keeps an encrypted copy on this machine, unlocked by a key only you hold.",
        )}
      />

      <SettingsSection
        title={t("What is protected")}
        description={t(
          "The sealed copy sits beside the database. Losing it does not affect a running instance: the configuration keeps the values it started with.",
        )}
      >
        <SettingsRow
          label={t("Status")}
          description={t("A sealed copy is unlocked when the service starts.")}
        >
          <div className="flex items-center justify-end gap-3">
            {status.isPending ? <Spinner size="sm" /> : null}
            {sealed ? (
              <Chip color="success" variant="tertiary">
                {current?.mode === "prompt"
                  ? t("Ask for the key at every start")
                  : t("Remember the key on this machine")}
              </Chip>
            ) : (
              <Chip color="warning" variant="tertiary">
                {t("Not sealed")}
              </Chip>
            )}
          </div>
        </SettingsRow>
        <SettingsRow label={t("Secrets")} align="start">
          <div className="flex flex-wrap justify-end gap-2">
            {(current?.secrets ?? []).map((name) => (
              <Chip key={name} variant="tertiary">
                {name}
              </Chip>
            ))}
          </div>
        </SettingsRow>
      </SettingsSection>

      <SettingsSection
        title={sealed ? t("Seal it again") : t("Seal the secrets")}
        description={
          sealed
            ? t(
                "Sealing again replaces the file and the key. The old key stops working immediately.",
              )
            : t("Choose a key you will still have if this machine is lost.")
        }
      >
        <SettingsRow
          label={t("Key")}
          description={t("At least {{count}} characters. Leave it empty to have one generated.", {
            count: current?.minimumKeyLength ?? 8,
          })}
          align="start"
        >
          <div className="flex w-full flex-col items-end gap-3">
            <TextField className="w-72" isInvalid={tooShort}>
              <Label className="sr-only">{t("Key")}</Label>
              <Input
                type="password"
                autoComplete="new-password"
                value={key}
                placeholder={t("Key")}
                onChange={(event) => setKey(event.currentTarget.value)}
              />
            </TextField>
            <Select
              aria-label={t("Key handling")}
              className="w-72"
              selectedKey={mode}
              isDisabled={busy}
              onSelectionChange={(selection) => setMode(String(selection) as VaultKeyMode)}
            >
              <Label className="sr-only">{t("Key handling")}</Label>
              <Select.Trigger>
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {(["saved", "prompt"] as VaultKeyMode[]).map((value) => (
                    <ListBox.Item key={value} id={value} textValue={t(MODE_LABELS[value])}>
                      {t(MODE_LABELS[value])}
                    </ListBox.Item>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
            <Button
              variant="secondary"
              isDisabled={busy || tooShort}
              onPress={() => create.mutate()}
            >
              {create.isPending ? t("Sealing") : t("Seal the secrets")}
            </Button>
          </div>
        </SettingsRow>

        {issuedKey ? (
          <SettingsRow
            label={t("Your key")}
            description={t("Copy it now. It is shown once and never again.")}
            align="start"
          >
            <div className="flex flex-col items-end gap-3">
              <code className="rounded-lg border border-border px-3 py-2 font-mono text-sm">
                {issuedKey}
              </code>
              <Button
                variant="secondary"
                onPress={() => {
                  void copyText(issuedKey).then(
                    () => toast.success(t("Key copied")),
                    () => toast.error(t("The key could not be copied")),
                  );
                }}
              >
                <CopyIcon className="size-4" />
                {t("Copy key")}
              </Button>
            </div>
          </SettingsRow>
        ) : null}
      </SettingsSection>

      {sealed ? (
        <SettingsSection
          title={t("Undo")}
          description={t(
            "Neither action changes the running service; they change what the next start can open.",
          )}
        >
          <SettingsRow
            label={t("Forget the key here")}
            description={t(
              "Removes the remembered key so the next start asks for it. The sealed file stays.",
            )}
          >
            <Button
              variant="tertiary"
              isDisabled={busy || current?.mode === "prompt"}
              onPress={() => forget.mutate()}
            >
              {t("Forget the key here")}
            </Button>
          </SettingsRow>
          <SettingsRow
            label={t("Remove the sealed copy")}
            description={t(
              "Deletes the sealed file and its key. The configuration keeps the plaintext values, so the drive keeps working.",
            )}
          >
            <Button variant="danger" isDisabled={busy} onPress={() => setConfirmDestroy(true)}>
              {t("Remove the sealed copy")}
            </Button>
          </SettingsRow>
        </SettingsSection>
      ) : null}

      <Modal.Backdrop
        isOpen={confirmDestroy}
        onOpenChange={setConfirmDestroy}
        isDismissable={!destroy.isPending}
      >
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>{t("Remove the sealed copy?")}</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="flex flex-col gap-4 py-4">
              <Typography.Paragraph className="text-sm text-muted">
                {t(
                  "The secrets go back to living only in config.toml, in the clear, and any snapshot carrying the vault key can no longer be opened.",
                )}
              </Typography.Paragraph>
              <div className="flex justify-end gap-3">
                <Button
                  variant="tertiary"
                  isDisabled={destroy.isPending}
                  onPress={() => setConfirmDestroy(false)}
                >
                  {t("Cancel")}
                </Button>
                <Button
                  variant="danger"
                  isDisabled={destroy.isPending}
                  onPress={() => destroy.mutate()}
                >
                  {destroy.isPending ? t("Removing") : t("Remove")}
                </Button>
              </div>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </>
  );
}
