import { useCallback, useEffect, useState } from "react";

import { Button } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import { Switch } from "~/components/ui/switch";
import { disable, enable, sendTest, status, type PushStatus } from "~/lib/push";

/**
 * Turning notifications on for this device.
 *
 * Deliberately per-device, and it says so. Every other setting in Omniplex is
 * shared across paired devices; this one cannot be, because a push
 * subscription belongs to one browser on one machine. Someone who turned it on
 * for their phone and then wonders why the laptop is silent is asking a fair
 * question, and the copy answers it rather than leaving them to guess.
 */
export function NotificationSettings() {
  const [state, setState] = useState<PushStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tested, setTested] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setState(await status());
    } catch (e) {
      setState({ state: "off" });
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = async (on: boolean) => {
    setBusy(true);
    setError(null);
    setTested(false);
    try {
      setState(on ? await enable() : await disable());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  // After a trip to browser settings the permission has changed but nothing in
  // the page knows: there is no event for it. This is the "I fixed it, look
  // again" button.
  const recheck = async () => {
    setBusy(true);
    setError(null);
    await refresh();
    setBusy(false);
  };

  const test = async () => {
    setBusy(true);
    setError(null);
    try {
      await sendTest();
      setTested(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (!state) return null;

  const on = state.state === "on";
  // "denied" and "needs-install" are not things a button can fix — only the
  // user can, in browser settings or by installing the app — so the control
  // is not offered, and the reason is shown in its place.
  const actionable = state.state === "on" || state.state === "off";

  return (
    <>
      <Separator />
      <div className="space-y-2.5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-[13px]">Notifications on this device</div>
            <p className="text-muted-foreground text-[12px]">
              When a turn finishes or a session needs an answer and nothing is open in front of
              you.
            </p>
          </div>
          {actionable && (
            <Switch
              checked={on}
              disabled={busy}
              onCheckedChange={(next) => void toggle(next)}
              aria-label="Notifications on this device"
            />
          )}
        </div>

        {/* "denied" has its own, more specific block below; showing the
            generic reason too would say "this is blocked" twice running. */}
        {state.reason && !actionable && state.state !== "denied" && (
          <p className="bg-muted text-muted-foreground rounded-lg p-2.5 text-[12px]">
            {state.reason}
          </p>
        )}

        {/* A denial is the one state the app cannot argue with: the browser
            will not raise its dialog again, and nothing here can reset it. All
            that is honest is to say where the switch actually lives, and to
            offer to look again once they have been. */}
        {state.state === "denied" && (
          <div className="space-y-2">
            <p className="text-muted-foreground text-[12px]">
              Only the browser can undo this. On iOS: Settings → Notifications → Omniplex. In a
              desktop browser: the icon at the left of the address bar → Notifications.
            </p>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => void recheck()}>
              Check again
            </Button>
          </div>
        )}

        {on && (
          <div className="flex items-center gap-2">
            <Button size="sm" variant="outline" disabled={busy} onClick={() => void test()}>
              Send a test
            </Button>
            {tested && (
              <span className="text-muted-foreground text-[12px]">
                Sent — it should arrive shortly.
              </span>
            )}
          </div>
        )}

        {on && (
          <p className="text-muted-foreground text-[12px]">
            Each device is turned on separately, and a device that has the app open on screen gets
            a message in the app instead.
          </p>
        )}

        {error && <p className="text-destructive text-[12px]">{error}</p>}
      </div>
    </>
  );
}
