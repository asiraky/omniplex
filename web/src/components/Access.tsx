import { ExternalLinkIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { NotificationSettings } from "~/components/NotificationSettings";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Separator } from "~/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";
import { cn } from "~/lib/utils";
import type { Access as AccessInfo, Endpoint } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

const reachLabel: Record<string, string> = {
  loopback: "this machine only",
  lan: "same network only",
  overlay: "anywhere, via Tailscale",
  public: "the open internet",
};

/**
 * Switching to another address is a navigation, not a reconnection.
 *
 * A device token is bound to the origin it was paired on — cookies are scoped
 * per host — so arriving at a different address means pairing it once. The
 * panel says so rather than letting the user discover it as a bounce to the
 * pairing screen.
 */
function EndpointRow({ endpoint, current }: { endpoint: Endpoint; current: boolean }) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-lg border px-3 py-2.5",
        current && "border-primary/50 bg-primary/5",
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-[13px]">{endpoint.label}</span>
          {endpoint.stable && (
            <Badge variant="secondary" className="rounded px-1.5 py-0 font-mono text-[9px] uppercase">
              stable
            </Badge>
          )}
          {endpoint.encrypted === false && (
            <Badge
              variant="outline"
              className="border-attention/40 text-attention-foreground rounded px-1.5 py-0 font-mono text-[9px] uppercase"
            >
              unencrypted
            </Badge>
          )}
          {current && <span className="text-primary ml-auto font-mono text-[10px]">connected</span>}
        </div>
        <p className="text-muted-foreground mt-0.5 font-mono text-[11px] break-all">
          {endpoint.url}
        </p>
        <p className="text-muted-foreground mt-0.5 text-[11px]">
          {reachLabel[endpoint.reachability] ?? endpoint.reachability}
        </p>
        {endpoint.encrypted === false && (
          // We cannot issue a certificate a browser trusts for a LAN address,
          // so the honest thing is to say what crossing it costs rather than
          // train people to click through a warning.
          <p className="text-attention-foreground mt-1 text-[11px]">
            Traffic here is not encrypted. On a network you do not control, the pairing code and
            this device's token can be read off the wire. The Tailscale address avoids that.
          </p>
        )}
      </div>

      {!current && (
        <Button asChild variant="outline" size="sm" className="mt-0.5 shrink-0">
          <a href={endpoint.url}>
            Open
            <ExternalLinkIcon />
          </a>
        </Button>
      )}
    </div>
  );
}

function AccessBody({
  access,
  onEnableHTTPS,
  onDisableHTTPS,
}: {
  access: AccessInfo;
  onEnableHTTPS: () => Promise<void>;
  onDisableHTTPS: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const { overlay } = access;
  const stable = access.endpoints.find((e) => e.stable);

  return (
    <>
      {stable && (
        <p className="bg-muted text-muted-foreground rounded-lg p-2.5 text-[12px]">
          Bookmark the <span className="text-foreground">{stable.label}</span> address — it keeps
          working when you leave the house, and it does not change when your network does.
        </p>
      )}

      <div className="space-y-2">
        {access.endpoints.map((e) => (
          <EndpointRow key={e.id} endpoint={e} current={sameOrigin(e.url, location.origin)} />
        ))}
      </div>

      <p className="text-muted-foreground text-[11px]">
        Opening a different address asks you to pair once there: a paired device is remembered per
        address.
      </p>

      <Separator />

      <div>
        <h3 className="text-[13px] font-medium">Remote access</h3>

        {!overlay.installed && (
          <p className="text-muted-foreground mt-1.5 text-[12px]">
            Tailscale is not installed. With it, this server gets a fixed name you can open from
            anywhere — no ports opened, no address to remember.{" "}
            <a
              href="https://tailscale.com/download"
              target="_blank"
              rel="noreferrer"
              className="text-primary underline underline-offset-4"
            >
              Install it
            </a>
            .
          </p>
        )}

        {overlay.installed && !overlay.running && (
          <p className="text-muted-foreground mt-1.5 text-[12px]">
            Tailscale is installed but not signed in. Sign in and reopen this panel.
          </p>
        )}

        {overlay.running && (
          <>
            <p className="mt-1.5 font-mono text-[11px] break-all">{overlay.dnsName}</p>

            {overlay.https ? (
              <>
                <p className="text-muted-foreground mt-2 text-[12px]">
                  Served over HTTPS with a real certificate, so this page can be installed to your
                  home screen.
                </p>
                <Button
                  variant="outline"
                  className="mt-3"
                  disabled={busy}
                  onClick={() => run(onDisableHTTPS)}
                  title="Runs: tailscale serve --https=443 off"
                >
                  {busy ? "Working…" : "Turn off HTTPS"}
                </Button>
              </>
            ) : (
              <>
                <p className="text-muted-foreground mt-2 text-[12px]">
                  Traffic over Tailscale is already encrypted end to end. Turning on HTTPS adds a
                  real certificate, which is what browsers require before allowing home-screen
                  install and notifications.
                </p>
                <p className="text-muted-foreground mt-1.5 text-[11px]">
                  This changes your machine's Tailscale configuration and persists across restarts.
                  Undo it here, or with{" "}
                  <code className="font-mono">tailscale serve --https=443 off</code>.
                </p>
                <Button className="mt-3" disabled={busy} onClick={() => run(onEnableHTTPS)}>
                  {busy ? "Working…" : "Turn on HTTPS"}
                </Button>
              </>
            )}
          </>
        )}

        {error && (
          <Alert variant="destructive" className="mt-3">
            <AlertDescription className="font-mono text-[12px]">{error}</AlertDescription>
          </Alert>
        )}
      </div>

      {/* Notifications live here because this is already the panel about this
          device and how it reaches the server — and because the HTTPS control
          directly above is the thing that has to be on before they can work. */}
      <NotificationSettings />
    </>
  );
}

export function AccessPanel({
  access,
  onEnableHTTPS,
  onDisableHTTPS,
  onClose,
}: {
  access: AccessInfo;
  onEnableHTTPS: () => Promise<void>;
  onDisableHTTPS: () => Promise<void>;
  onClose: () => void;
}) {
  const isDesktop = useIsDesktop();
  const title = "Reaching this server";
  const description = "Every address Omniplex is currently listening on.";
  const body: ReactNode = (
    <AccessBody access={access} onEnableHTTPS={onEnableHTTPS} onDisableHTTPS={onDisableHTTPS} />
  );
  const onOpenChange = (open: boolean) => !open && onClose();

  // On a phone this arrives from the bottom, where a thumb already is; on a
  // pointer it is an ordinary centred dialog. Same content either way.
  if (!isDesktop) {
    return (
      <Sheet open onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          className="scroll-thin max-h-[85dvh] overflow-y-auto pb-[calc(1rem+env(safe-area-inset-bottom))]"
        >
          <SheetHeader>
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{description}</SheetDescription>
          </SheetHeader>
          <div className="space-y-3 px-4 pb-4">{body}</div>
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="scroll-thin max-h-[85dvh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">{body}</div>
      </DialogContent>
    </Dialog>
  );
}

/** Compares origins so the current endpoint is marked whichever form it took. */
function sameOrigin(url: string, origin: string): boolean {
  try {
    return new URL(url).origin === origin;
  } catch {
    return false;
  }
}
