import { Button, Chip, Input, Label, Spinner, TextField, Typography } from "@heroui/react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import {
  clearDeviceToken,
  POLL_INTERVAL_MS,
  pollAccess,
  readDeviceToken,
  requestAccess,
  suggestedDeviceName,
  writeDeviceToken,
} from "@/features/lan/request";
import { t } from "@/i18n";

type Phase = "idle" | "waiting" | "approved" | "denied";

/**
 * The panel a device that is not on this machine sees.
 *
 * It exists because the alternative is worse: without it, a device that reaches
 * the drive over the network lands on a sign-in form it cannot use, and the
 * person holding it has no way to find out that somebody has to approve it. The
 * whole conversation is: ask, wait, then reload into the session the drive lends
 * it.
 */
export function NetworkAccessPanel() {
  const [name, setName] = useState(() => suggestedDeviceName());
  const [phase, setPhase] = useState<Phase>("idle");
  const [pendingId, setPendingId] = useState("");
  const timer = useRef<number | null>(null);

  // Polling stops the moment the component goes away, so a device that navigates
  // off this page does not keep asking the drive a question nobody is waiting
  // for an answer to.
  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current);
    };
  }, []);

  const waitForAnswer = (id: string) => {
    const tick = async () => {
      try {
        const claim = await pollAccess(id);
        if (claim.state === "pending") {
          timer.current = window.setTimeout(tick, POLL_INTERVAL_MS);
          return;
        }
        if (claim.state === "denied") {
          setPhase("denied");
          return;
        }
        if (claim.token) {
          writeDeviceToken(claim.token);
          setPhase("approved");
          toast.success(t("This device was approved"));
          // A full reload rather than a client-side navigation: everything this
          // page has already fetched was refused, and the session changes
          // underneath it.
          window.setTimeout(() => window.location.reload(), 600);
          return;
        }
        // Approved but the credential was already collected once. Asking again
        // is the only way forward, and the person decides again.
        setPhase("denied");
      } catch {
        setPhase("idle");
        toast.error(t("This request is no longer waiting. Ask again."));
      }
    };
    void tick();
  };

  const ask = async () => {
    setPhase("waiting");
    try {
      const request = await requestAccess(name);
      setPendingId(request.id);
      toast.success(t("Ask the person at the drive to approve this device."));
      waitForAnswer(request.id);
    } catch (error) {
      setPhase("idle");
      toast.error(t("Access could not be requested"), {
        description: error instanceof Error ? error.message : undefined,
      });
    }
  };

  if (phase === "approved") {
    return (
      <div className="flex flex-col items-center gap-3 py-6 text-center">
        <Spinner size="lg" />
        <Typography.Paragraph className="text-sm text-muted">
          {t("Approved. Loading the drive...")}
        </Typography.Paragraph>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Chip color={phase === "waiting" ? "warning" : "default"} variant="tertiary">
          {phase === "waiting"
            ? t("Waiting for approval")
            : phase === "denied"
              ? t("Not approved")
              : t("Not approved")}
        </Chip>
      </div>
      <Typography.Paragraph className="text-sm text-muted">
        {t(
          "This drive is on somebody else's computer. It is reachable from this network, but it will not work until the person at that computer approves this device.",
        )}
      </Typography.Paragraph>

      {phase === "denied" ? (
        <Typography.Paragraph className="text-sm text-warning">
          {t("This device was refused or the request expired. You can ask again.")}
        </Typography.Paragraph>
      ) : null}

      <TextField className="grid gap-1">
        <Label>{t("What should this device be called?")}</Label>
        <Input
          value={name}
          spellCheck={false}
          onChange={(event) => setName(event.currentTarget.value)}
        />
        <Typography.Paragraph className="text-xs text-muted">
          {t(
            "This is what the person at the drive will see, along with where the request came from.",
          )}
        </Typography.Paragraph>
      </TextField>

      <Button
        className="w-full"
        isDisabled={phase === "waiting" || name.trim().length === 0}
        onPress={() => void ask()}
      >
        {phase === "waiting" ? <Spinner size="sm" /> : null}
        {phase === "waiting"
          ? t("Waiting for approval")
          : phase === "idle"
            ? t("Ask for access")
            : t("Ask again")}
      </Button>

      {pendingId ? (
        <Typography.Paragraph className="text-center text-xs text-muted">
          {t("Request {{id}} - the person at the drive sees it as a card to approve.", {
            id: pendingId.slice(0, 8),
          })}
        </Typography.Paragraph>
      ) : null}

      {readDeviceToken() ? (
        <Button
          variant="tertiary"
          className="w-full"
          onPress={() => {
            clearDeviceToken();
            window.location.reload();
          }}
        >
          {t("Forget the approval on this device")}
        </Button>
      ) : null}
    </div>
  );
}
