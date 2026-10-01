import { KeyRoundIcon, RefreshCwIcon, RotateCwIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { StatusChip, type Tone } from "~/components/connections/parts";
import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { canReconnect, ownsSignIn } from "~/lib/connections";
import { cn } from "~/lib/utils";
import type { Connections, McpServer, ThreadMcpReport, ThreadMcpStatus } from "~/protocol";
import { useLatest } from "~/useLatest";

const CHIP: Record<ThreadMcpStatus, { label: string; tone: Tone }> = {
  connected: { label: "Connected", tone: "good" },
  needs_auth: { label: "Sign in", tone: "warn" },
  failed: { label: "Failed", tone: "bad" },
  pending: { label: "Starting", tone: "warn" },
  disabled: { label: "Off", tone: "warn" },
};

export interface McpSurfaceProps {
  threadId: string;
  /** thread_mcp_status, thread_mcp_reconnect, list_connections. */
  command: (command: string, args: unknown) => Promise<any>;
  /** Open Settings → Connections on this server. */
  onOpenConnections?: (server: string) => void;
}

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/**
 * The running session's own report on each MCP server it was given. Read on
 * open and on refresh, never polled: a reconnect answers with the new report.
 */
export function McpSurface({ threadId, command, onOpenConnections }: McpSurfaceProps) {
  const [report, setReport] = useState<ThreadMcpReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  // Omniplex's own servers. Only those can be reconnected or signed in from
  // here; the rest come from the agent's own config and are its business.
  const [ours, setOurs] = useState<McpServer[] | null>(null);
  const commandRef = useLatest(command);

  const refresh = useCallback(() => {
    setLoading(true);
    setError("");
    commandRef
      .current("thread_mcp_status", { threadId })
      .then((r: ThreadMcpReport) => setReport({ live: !!r?.live, servers: r?.servers ?? [] }))
      .catch((e: unknown) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [threadId, commandRef]);

  useEffect(refresh, [refresh]);

  useEffect(() => {
    let stale = false;
    commandRef
      .current("list_connections", {})
      .then((r: Connections) => !stale && setOurs(r?.servers ?? []))
      .catch(() => {});
    return () => {
      stale = true;
    };
  }, [commandRef]);

  const reconnect = async (name: string) => {
    setBusy(name);
    setError("");
    try {
      const r = (await command("thread_mcp_reconnect", { threadId, name })) as ThreadMcpReport;
      setReport({ live: !!r?.live, servers: r?.servers ?? [] });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 border-b px-2 py-1">
        <p className="text-muted-foreground min-w-0 flex-1 truncate px-1 text-[12px]">
          What this thread's agent reports
        </p>
        <IconButton label="Refresh MCP servers" onClick={refresh} disabled={loading}>
          <RefreshCwIcon className={cn(loading && "animate-spin")} />
        </IconButton>
      </div>

      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-2">
        {error && <p className="text-destructive px-1 py-2 text-[12px] break-words">{error}</p>}
        {loading && !report && (
          <p className="text-muted-foreground flex items-center justify-center gap-2 py-10 text-[12px]">
            <Spinner className="text-primary size-3.5" /> Asking the agent…
          </p>
        )}
        {report && !report.live && (
          <div className="text-muted-foreground px-4 py-10 text-center text-[12px]">
            <p>No running session.</p>
            <p className="mt-1">Send a message and the agent starts with its MCP servers.</p>
          </div>
        )}
        {report?.live && report.servers.length === 0 && (
          <p className="text-muted-foreground px-4 py-10 text-center text-[12px]">
            This session has no MCP servers.
          </p>
        )}
        {report?.live && report.servers.length > 0 && (
          <ul className="divide-y overflow-hidden rounded-lg border">
            {report.servers.map((s) => {
              const chip = CHIP[s.status];
              const own = !!ours?.some((o) => o.name === s.name);
              const signIn =
                s.status === "needs_auth" && !!onOpenConnections && !!ours && ownsSignIn(s.name, ours);
              const again = own && canReconnect(s.status);
              return (
                <li key={s.name} className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2">
                  <div className="min-w-0 flex-1">
                    <p className="flex flex-wrap items-center gap-1.5">
                      <span className="truncate font-mono text-[12px]">{s.name}</span>
                      <StatusChip label={chip?.label} tone={chip?.tone} />
                    </p>
                    {ours && !own && canReconnect(s.status) && (
                      <p className="text-muted-foreground text-[11px]">
                        From the agent's own config. Fix it there.
                      </p>
                    )}
                    {s.error && <Folded text={s.error} />}
                  </div>
                  {(signIn || again) && (
                    <div className="flex shrink-0 gap-2">
                      {signIn && (
                        <Button size="sm" onClick={() => onOpenConnections?.(s.name)}>
                          <KeyRoundIcon />
                          Sign in
                        </Button>
                      )}
                      {again && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy !== null}
                          onClick={() => void reconnect(s.name)}
                        >
                          {busy === s.name ? <Spinner aria-hidden className="size-3.5" /> : <RotateCwIcon />}
                          Reconnect
                        </Button>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

/** A long error folded to two lines; a tap shows the rest. */
function Folded({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <button
      type="button"
      onClick={() => setOpen((o) => !o)}
      aria-expanded={open}
      className={cn(
        "text-muted-foreground block w-full text-left text-[11px] break-words",
        !open && "line-clamp-2",
      )}
    >
      {text}
    </button>
  );
}
