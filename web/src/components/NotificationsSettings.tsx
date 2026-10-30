import { BellOffIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { SettingsPane } from "~/components/SettingsPane";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Label } from "~/components/ui/label";
import { Separator } from "~/components/ui/separator";
import { Switch } from "~/components/ui/switch";
import {
  deviceLabel,
  fetchPush,
  localSubscription,
  pushSupport,
  readPushEnv,
  removePushDevice,
  savePushDevice,
  sendTestPush,
  subscribePush,
  testSummary,
  type PushState,
  type PushSupport,
} from "~/lib/push";

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));

const UNSUPPORTED: Record<Exclude<PushSupport, "ok">, string> = {
  "ios-install":
    "On iPhone and iPad, notifications only work from the Home Screen app. In Safari, tap Share, then Add to Home Screen (iOS 16.4 or later), and open Omniplex from there.",
  insecure:
    "This browser can't receive notifications here: push needs an HTTPS address. Open Omniplex over https:// to turn them on.",
  unsupported: "This browser can't receive push notifications.",
};

const added = (ms: number) =>
  new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });

/**
 * Web push: this device on or off, every device the server pushes to, and a
 * test. Agents send through the notify tool; the server skips a push while
 * any device has that thread on screen.
 */
export function NotificationsSettings({ onBack }: { onBack?: () => void }) {
  const [support] = useState(() => pushSupport(readPushEnv()));
  const [push, setPush] = useState<PushState | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  // This browser's subscription endpoint, if it holds one.
  const [local, setLocal] = useState<string | null>(null);
  const [permission, setPermission] = useState<NotificationPermission | null>(() =>
    support === "ok" ? Notification.permission : null,
  );
  // What is in flight: "toggle", "test", or the id of a device being removed.
  // One thing at a time; every control waits while anything is.
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoadError(null);
    try {
      setPush(await fetchPush());
    } catch (e) {
      setLoadError(message(e));
    }
  }, []);

  useEffect(() => {
    void load();
    if (support !== "ok") return;
    localSubscription().then(
      (sub) => setLocal(sub?.endpoint ?? null),
      () => {},
    );
  }, [load, support]);

  const run = async (what: string, work: () => Promise<void>) => {
    setBusy(what);
    setError(null);
    setTestResult(null);
    try {
      await work();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(null);
    }
  };

  const devices = push?.subscriptions ?? [];
  const mine = local ? devices.find((d) => d.endpoint === local) : undefined;
  const on = !!mine;

  const turnOn = () => {
    // The very first thing the tap does, with no await before it: iOS only
    // shows the permission prompt from inside the user's gesture.
    const asked = Notification.requestPermission();
    void run("toggle", async () => {
      const result = await asked;
      setPermission(result);
      if (result !== "granted") return;
      const sub = await subscribePush(push!.publicKey);
      await savePushDevice(sub, deviceLabel(navigator.userAgent, navigator.maxTouchPoints));
      setLocal(sub.endpoint);
      setPush(await fetchPush());
    });
  };

  // Off on this device, or Remove on any row. The server's row goes first: if
  // that fails, this browser stays subscribed and the list stays true.
  const remove = (id: string) =>
    run(id, async () => {
      await removePushDevice(id);
      if (id === mine?.id) {
        const sub = await localSubscription();
        await sub?.unsubscribe();
        setLocal(null);
      }
      setPush((p) => p && { ...p, subscriptions: p.subscriptions.filter((d) => d.id !== id) });
    });

  return (
    <SettingsPane title="Notifications" onBack={onBack}>
      <p className="text-muted-foreground text-[13px]">
        Agents can notify you when they need you, using the notify tool. Nothing is sent while you
        have that thread open on screen.
      </p>

      {support === "ok" ? (
        <div className="space-y-1.5">
          <div className="flex items-start gap-3">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label htmlFor="settings-push-this-device">Notifications on this device</Label>
              {permission === "denied" && !on ? (
                <p className="text-attention-foreground text-[11px]">
                  Blocked for this site. Allow notifications for Omniplex in your browser's site
                  settings, or on a phone in the system Settings app under Notifications, then
                  turn this on again.
                </p>
              ) : (
                <p className="text-muted-foreground text-[11px]">
                  {on
                    ? "This device gets a notification when an agent asks for you."
                    : "Asks for permission, then adds this device to the list below."}
                </p>
              )}
            </div>
            <Switch
              id="settings-push-this-device"
              checked={on}
              disabled={!push || busy !== null}
              onCheckedChange={(next) => {
                if (next) turnOn();
                else if (mine) void remove(mine.id);
              }}
            />
          </div>
        </div>
      ) : (
        <Alert>
          <BellOffIcon />
          <AlertDescription className="text-[12px]">{UNSUPPORTED[support]}</AlertDescription>
        </Alert>
      )}

      {error && (
        <p role="alert" className="text-attention-foreground text-[11px] break-words">
          {error}
        </p>
      )}

      <Separator />

      <div className="space-y-2">
        <h3 className="text-[12px] font-medium">Devices</h3>
        {loadError ? (
          <div className="flex items-center gap-2">
            <p role="alert" className="text-attention-foreground min-w-0 flex-1 text-[12px]">
              {loadError}
            </p>
            <Button variant="outline" onClick={() => void load()}>
              Retry
            </Button>
          </div>
        ) : !push ? (
          <p className="text-muted-foreground text-[12px]">Loading…</p>
        ) : devices.length === 0 ? (
          <p className="text-muted-foreground text-[12px]">
            No devices yet. Turn notifications on from each phone or computer you want them on.
          </p>
        ) : (
          <ul className="divide-y">
            {devices.map((d) => (
              <li key={d.id} className="flex items-center gap-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="flex min-w-0 items-center gap-2 text-[13px]">
                    <span className="truncate">{d.label || "Unnamed device"}</span>
                    {d.id === mine?.id && <Badge variant="secondary">This device</Badge>}
                  </p>
                  <p className="text-muted-foreground text-[11px]">Added {added(d.createdAt)}</p>
                </div>
                <Button
                  variant="outline"
                  disabled={busy !== null}
                  onClick={() => void remove(d.id)}
                  aria-label={`Remove ${d.label || "device"}`}
                >
                  {busy === d.id ? "Removing…" : "Remove"}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {devices.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <Button
            variant="outline"
            disabled={busy !== null}
            onClick={() =>
              void run("test", async () => setTestResult(testSummary(await sendTestPush())))
            }
          >
            {busy === "test" ? "Sending…" : "Send test notification"}
          </Button>
          {testResult && (
            <p role="status" className="text-muted-foreground text-[12px]">
              {testResult}
            </p>
          )}
        </div>
      )}
    </SettingsPane>
  );
}
