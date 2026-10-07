import {
  Button,
  Card,
  Description,
  FieldError,
  Input,
  Label,
  Spinner,
  Switch,
  Tabs,
  TextField,
} from "@heroui/react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { toast } from "sonner";
import PhoneIcon from "~icons/gravity-ui/person";
import QrIcon from "~icons/gravity-ui/qr-code";
import KeyIcon from "~icons/gravity-ui/key";
import ShieldIcon from "~icons/gravity-ui/shield-check";
import { $api } from "@/api/client";
import { userMessage } from "@/api/errors";
import { newIdempotencyKey } from "@/features/shared/idempotency";
import { getQueryClient } from "@/lib/queryClient";
import { currentUserQueryOptions } from "@/auth/queries";
import {
  clearApiKey,
  readApiKey,
  telegramSignInAllowed,
  writeApiKey,
} from "@/auth/api-key";
import { t } from "@/i18n";
import { LocaleSelect } from "@/i18n/LocaleSelect";

type Step = "phone" | "code" | "password";
type Method = "key" | "phone" | "qr";
type Flow = {
  flowId: string;
  expiresAt: string;
  passwordRequired?: boolean;
  state?: string;
  qrUrl?: string;
  qrExpiresAt?: string;
};
type CookieSession = { authenticated: true; expiresAt: string };

// Telegram expects E.164. Accept what people actually type (spaces, dashes,
// parentheses, a leading 00 international prefix) and send the canonical form.
const E164_PATTERN = /^\+[1-9][0-9]{7,14}$/;
const PHONE_EXAMPLE = "+8613800138000";

function normalizePhone(value: string) {
  const stripped = value.replace(/[\s()\-.]/g, "");
  return stripped.startsWith("00") ? `+${stripped.slice(2)}` : stripped;
}

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>) => ({
    redirect:
      typeof search.redirect === "string" && search.redirect.startsWith("/")
        ? search.redirect
        : "/files",
  }),
  component: LoginPage,
});

function isSession(value: unknown): value is CookieSession {
  return Boolean(value && typeof value === "object" && "authenticated" in value);
}

function LoginPage() {
  const navigate = useNavigate();
  const { redirect } = Route.useSearch();
  // A published origin is a public surface: it offers key sign-in only. The
  // Telegram flow stays available on the machine that owns the account.
  const telegramAvailable = telegramSignInAllowed();
  const [method, setMethod] = useState<Method>(telegramAvailable ? "phone" : "key");
  const [step, setStep] = useState<Step>("phone");
  const [flowId, setFlowId] = useState("");
  const [phone, setPhone] = useState("");
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [qrUrl, setQrUrl] = useState("");
  const [qrExpiry, setQrExpiry] = useState("");
  const [apiKey, setApiKey] = useState(() => readApiKey() ?? "");
  const [rememberDevice, setRememberDevice] = useState(false);
  const [keyPending, setKeyPending] = useState(false);

  const startPhone = $api.useMutation("post", "/v1/auth/telegram/start");
  const verifyCode = $api.useMutation("post", "/v1/auth/cookie/telegram/verify-code");
  const verifyPassword = $api.useMutation("post", "/v1/auth/cookie/telegram/verify-password");
  const startQr = $api.useMutation("post", "/v1/auth/telegram/qr/start");
  const pollQr = $api.useMutation("post", "/v1/auth/cookie/telegram/qr/poll");
  const pending = startPhone.isPending || verifyCode.isPending || verifyPassword.isPending;
  const phoneValue = normalizePhone(phone);
  const phoneInvalid = phoneValue.length > 0 && !E164_PATTERN.test(phoneValue);

  const finish = async () => {
    const query = currentUserQueryOptions();
    const qc = getQueryClient();
    await qc.invalidateQueries({ queryKey: query.queryKey });
    await qc.ensureQueryData(query);
    toast.success(t("Signed in to Teldrive"));
    await navigate({ to: redirect, replace: true });
  };

  // Sign in by proving the key works: store it, then let the same /v1/me call
  // the router guard uses decide. A rejected key is forgotten again so the next
  // visit starts clean.
  const submitKey = async () => {
    const value = apiKey.trim();
    if (!value) return;
    setKeyPending(true);
    writeApiKey(value, rememberDevice);
    try {
      await finish();
    } catch (error) {
      clearApiKey();
      toast.error(t("API key sign-in failed"), { description: userMessage(error) });
    } finally {
      setKeyPending(false);
    }
  };

  const submitPhone = async () => {
    try {
      if (step === "phone") {
        const result = (await startPhone.mutateAsync({
          params: { header: { "Idempotency-Key": newIdempotencyKey() } },
          body: { phoneNumber: phoneValue },
        })) as Flow;
        setFlowId(result.flowId);
        setStep(result.passwordRequired ? "password" : "code");
        return;
      }
      if (step === "code") {
        const result = await verifyCode.mutateAsync({
          params: { header: { "Idempotency-Key": newIdempotencyKey() } },
          body: { flowId, code: code.trim() },
        });
        if (isSession(result)) await finish();
        else setStep("password");
        return;
      }
      const result = await verifyPassword.mutateAsync({
        params: { header: { "Idempotency-Key": newIdempotencyKey() } },
        body: { flowId, password },
      });
      if (isSession(result)) await finish();
    } catch (error) {
      toast.error(t("Telegram sign-in failed"), { description: userMessage(error) });
    }
  };

  useEffect(() => {
    if (method !== "qr") return;
    let active = true;
    let timer = 0;
    void startQr
      .mutateAsync({ params: { header: { "Idempotency-Key": newIdempotencyKey() } } })
      .then((result) => {
        if (!active) return;
        const flow = result as Flow;
        setFlowId(flow.flowId);
        setQrUrl(flow.qrUrl ?? "");
        setQrExpiry(flow.qrExpiresAt ?? flow.expiresAt);
        timer = window.setInterval(async () => {
          try {
            const next = await pollQr.mutateAsync({
              params: { header: { "Idempotency-Key": newIdempotencyKey() } },
              body: { flowId: flow.flowId },
            });
            if (!active) return;
            if (isSession(next)) {
              window.clearInterval(timer);
              await finish();
              return;
            }
            const state = next as Flow;
            if (state.state === "password_required") {
              window.clearInterval(timer);
              setMethod("phone");
              setStep("password");
              return;
            }
            if (state.qrUrl) setQrUrl(state.qrUrl);
            if (state.qrExpiresAt) setQrExpiry(state.qrExpiresAt);
          } catch (error) {
            window.clearInterval(timer);
            toast.error(t("QR sign-in stopped"), { description: userMessage(error) });
          }
        }, 2500);
      })
      .catch((error) =>
        toast.error(t("Unable to create QR sign-in"), { description: userMessage(error) }),
      );
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [method]);

  const keyPanel = (
    <div className="space-y-4 pt-4">
      <TextField className="grid gap-1">
        <Label>{t("API key")}</Label>
        <Input
          autoFocus
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder={t("tdk_...")}
          value={apiKey}
          onChange={(event) => setApiKey(event.currentTarget.value)}
        />
        <Description>
          {t("Create one under Settings, API keys. The secret is shown only once.")}
        </Description>
      </TextField>
      <Switch isSelected={rememberDevice} onChange={setRememberDevice}>
        <Switch.Content>
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
          <Label>{t("Remember this device")}</Label>
        </Switch.Content>
      </Switch>
      <p className="text-xs text-muted">
        {rememberDevice
          ? t("The key is kept in this browser until you sign out.")
          : t("The key is kept for this tab only and is dropped when it closes.")}
      </p>
      <Button className="w-full" onPress={submitKey} isDisabled={keyPending || !apiKey.trim()}>
        {keyPending ? <Spinner size="sm" /> : <KeyIcon className="size-4" />}
        {t("Sign in")}
      </Button>
    </div>
  );

  return (
    <main className="grid min-h-dvh bg-background text-foreground lg:grid-cols-[minmax(0,1.1fr)_minmax(24rem,0.9fr)]">
      <div className="pointer-events-none fixed right-4 top-4 z-10">
        <LocaleSelect className="pointer-events-auto" />
      </div>
      <section className="hidden border-r border-border bg-sidebar/70 p-12 lg:flex lg:flex-col lg:justify-between">
        <div className="flex size-11 items-center justify-center rounded-xl bg-accent font-semibold text-accent-foreground">
          {t("TD")}
        </div>
        <div className="my-auto max-w-xl">
          <p className="mb-3 text-xs font-medium uppercase tracking-[0.16em] text-accent">
            Teldrive
          </p>
          <h1 className="text-4xl font-semibold tracking-tight">
            {t("Your Telegram-backed cloud drive.")}
          </h1>
          <p className="mt-4 max-w-lg text-sm leading-6 text-muted">
            {t("Manage files, uploads, background jobs, channels, bots, sessions, and API access from one focused interface.")}
          </p>
        </div>
      </section>
      <section className="flex items-center justify-center p-4 sm:p-8 lg:p-12">
        <Card className="w-full max-w-md border border-border bg-surface/90 shadow-xl">
          <Card.Header className="block px-6 pt-6">
            <Card.Title>
              {telegramAvailable ? t("Sign in to Teldrive") : t("Sign in with an API key")}
            </Card.Title>
            <Card.Description>
              {telegramAvailable
                ? t("Use your Telegram account, or an API key issued for this machine.")
                : t("This drive is published through a gateway. Sign in with an API key issued by its owner.")}
            </Card.Description>
          </Card.Header>
          <Card.Content className="space-y-5 px-6 pb-6">
            {telegramAvailable ? (
              <Tabs
                selectedKey={method}
                onSelectionChange={(key) => {
                  setMethod(key as Method);
                  setStep("phone");
                }}
              >
                <Tabs.ListContainer>
                  <Tabs.List aria-label={t("Sign-in method")}>
                    <Tabs.Tab id="key">
                      <KeyIcon className="size-4" /> {t("API key")}
                    </Tabs.Tab>
                    <Tabs.Tab id="phone">
                      <PhoneIcon className="size-4" /> {t("Phone")}
                    </Tabs.Tab>
                    <Tabs.Tab id="qr">
                      <QrIcon className="size-4" /> {t("QR code")}
                    </Tabs.Tab>
                  </Tabs.List>
                </Tabs.ListContainer>
                <Tabs.Panel id="key">{keyPanel}</Tabs.Panel>
                <Tabs.Panel id="phone" className="space-y-4 pt-4">
                  {step === "phone" && (
                    <TextField className="grid gap-1" isInvalid={phoneInvalid}>
                      <Label>{t("Telegram phone number")}</Label>
                      <Input
                        autoFocus
                        placeholder={PHONE_EXAMPLE}
                        value={phone}
                        onChange={(event) => setPhone(normalizePhone(event.target.value))}
                      />
                      {phoneInvalid ? (
                        <FieldError>{t("Start with + and the country code, e.g. {{PHONE_EXAMPLE}}", { PHONE_EXAMPLE })}</FieldError>
                      ) : (
                        <Description>{t("Include the country code, e.g. {{PHONE_EXAMPLE}}. Spaces and dashes are fine.", { PHONE_EXAMPLE })}</Description>
                      )}
                    </TextField>
                  )}
                  {step === "code" && (
                    <TextField className="grid gap-1">
                      <Label>{t("Telegram code")}</Label>
                      <Input
                        autoFocus
                        inputMode="numeric"
                        value={code}
                        onChange={(event) => setCode(event.target.value)}
                      />
                    </TextField>
                  )}
                  {step === "password" && (
                    <TextField className="grid gap-1">
                      <Label>{t("Two-step verification password")}</Label>
                      <Input
                        autoFocus
                        type="password"
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                      />
                    </TextField>
                  )}
                  <Button
                    className="w-full"
                    onPress={submitPhone}
                    isDisabled={
                      pending ||
                      (step === "phone"
                        ? !E164_PATTERN.test(phoneValue)
                        : step === "code"
                          ? !code.trim()
                          : !password)
                    }
                  >
                    {pending ? <Spinner size="sm" /> : <ShieldIcon className="size-4" />}
                    {step === "phone" ? t("Send code") : t("Verify and sign in")}
                  </Button>
                  {step !== "phone" && (
                    <Button
                      variant="ghost"
                      className="w-full"
                      onPress={() => {
                        setStep("phone");
                        setFlowId("");
                        setCode("");
                        setPassword("");
                      }}
                    >
                      {t("Start again")}
                    </Button>
                  )}
                </Tabs.Panel>
                <Tabs.Panel id="qr" className="space-y-4 pt-4">
                  <div className="grid min-h-72 place-items-center rounded-xl border border-border bg-white p-5 text-black">
                    {qrUrl ? (
                      <QRCodeSVG value={qrUrl} size={220} aria-label={t("Telegram sign-in QR code")} />
                    ) : (
                      <Spinner size="lg" />
                    )}
                  </div>
                  <div className="text-center">
                    <p className="font-medium">{t("Scan with Telegram")}</p>
                    <p className="mt-1 text-xs text-muted">
                      {t("Settings → Devices → Link Desktop Device")}
                    </p>
                    <p className="mt-2 text-xs text-muted">
                      {t("Expires")} {qrExpiry ? new Date(qrExpiry).toLocaleTimeString() : "soon"}
                    </p>
                  </div>
                </Tabs.Panel>
              </Tabs>
            ) : (
              keyPanel
            )}
          </Card.Content>
        </Card>
      </section>
    </main>
  );
}
