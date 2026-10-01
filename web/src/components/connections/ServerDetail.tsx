import { KeyRoundIcon, LogOutIcon, PencilIcon, RefreshCwIcon, RotateCwIcon, Trash2Icon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import {
  ConfirmDialog,
  DetailHeader,
  DetailHeading,
  EditStrip,
  ErrorLine,
  FactList,
  FoldedProblem,
  type PageCommand,
} from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import {
  canReconnect,
  formFromServer,
  harnessesFor,
  liveMark,
  offersSignIn,
  serverKind,
  serverMark,
  serverSaveArgs,
  type ServerForm,
} from "~/lib/connections";
import { formatAge } from "~/lib/usageFormat";
import { cn, errorText } from "~/lib/utils";
import type { McpHarness, McpServer, ThreadMcp } from "~/protocol";

import { AgentSwitches, MarkChip, ServerFields } from "./parts";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";

function statusText(s: McpServer): string {
  if (serverKind(s) === "stdio" && s.status === "unchecked") {
    return "Omniplex starts this server inside each thread. Open the page from a thread to see how it is doing there.";
  }
  switch (s.status) {
    case "connected":
      return "Connected.";
    case "sign_in":
      return "Sign in to use this server.";
    case "failed":
      return "Could not reach this server.";
    case "unchecked":
      return "Not checked yet.";
  }
}

/**
 * One of Omniplex's servers: how it is doing, here and in the open thread,
 * which agents get it, and what it is. Edit swaps the body for the form.
 */
export function ServerDetail({
  server,
  harnesses,
  command,
  live,
  reconnecting,
  reconnectError,
  onReconnect,
  onBack,
  onSaved,
  onRemoved,
  onSignIn,
}: {
  server: McpServer;
  harnesses: McpHarness[];
  command: PageCommand;
  /** The open thread's report on this server, when it has one. */
  live?: ThreadMcp;
  reconnecting: boolean;
  /** Why the last reconnect of this server failed. */
  reconnectError?: string;
  onReconnect: () => void;
  onBack: () => void;
  onSaved: (s: McpServer, previousName?: string) => void;
  onRemoved: (name: string) => void;
  onSignIn: () => void;
}) {
  const formId = useId();
  const [busy, setBusy] = useState<"check" | "signout" | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<ServerForm | null>(null);
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  // The switches move at once and go back if the write fails. Each write
  // sends the whole list, so writes go one at a time, in order: two at once
  // can land out of order and save the earlier choice.
  const [off, setOff] = useState(server.off);
  useEffect(() => setOff(server.off), [server.off]);
  const writes = useRef<Promise<void>>(Promise.resolve());
  const queued = useRef(0);
  const confirmed = useRef<McpServer | null>(null);

  const kind = serverKind(server);
  const mark = serverMark(server);

  const run = async (what: NonNullable<typeof busy>, cmd: string) => {
    setBusy(what);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>(cmd, { name: server.name });
      if (res?.server) onSaved(res.server);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  const setAgents = (next: string[]) => {
    const name = server.name;
    setOff(next);
    setError("");
    queued.current++;
    writes.current = writes.current.then(async () => {
      try {
        const res = await command<{ server?: McpServer }>("set_mcp_server_off", { name, off: next });
        if (res?.server) confirmed.current = res.server;
      } catch (e) {
        setError(errorText(e));
      } finally {
        // Only the last write settles the switches: an earlier answer would
        // flick them back until the next one. After a failure they show what
        // the server last confirmed.
        if (--queued.current === 0) {
          if (confirmed.current) onSaved(confirmed.current);
          else setOff(server.off);
          confirmed.current = null;
        }
      }
    });
  };

  const save = async (form: ServerForm) => {
    const built = serverSaveArgs(form, server.name);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>("save_mcp_server", built.args);
      if (!res?.server) throw new Error("The server did not answer with the saved server.");
      setEditing(null);
      onSaved(res.server, server.name);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  const header = <DetailHeader backLabel="Back to MCP servers" onBack={onBack} title={server.name} sub={kind === "http" ? "Remote server" : "Runs as a command"} />;

  if (editing) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {header}
        <EditStrip label={`Editing ${server.name}`} formId={formId} saving={saving} onCancel={() => { setEditing(null); setError(""); }} />
        {error && <ErrorLine message={error} className="border-b px-3 py-2" />}
        <form
          id={formId}
          className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            void save(editing);
          }}
        >
          <ServerFields form={editing} onChange={(patch) => setEditing((f) => f && { ...f, ...patch })} />
        </form>
      </div>
    );
  }

  const offerSignIn = offersSignIn(server);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {header}
      <div className="scroll-thin min-h-0 flex-1 space-y-6 overflow-y-auto overscroll-contain px-3 py-3">
        <div className="space-y-3">
          <div
            className={cn(
              "space-y-1.5 rounded-lg border px-3 py-2",
              mark?.tone === "bad" && "border-destructive/30 bg-destructive/5",
              mark?.tone === "attention" && "bg-attention-surface",
            )}
          >
            <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] leading-snug">
              <MarkChip mark={mark} />
              <span>{statusText(server)}</span>
              {server.checkedAt && (
                <span className="text-muted-foreground text-[12px]">
                  Checked {formatAge(Date.parse(server.checkedAt), Date.now())}
                </span>
              )}
            </p>
            {server.error && <FoldedProblem problem={server.error} tone={server.status === "failed" ? "bad" : "attention"} />}
          </div>

          {error && <ErrorLine message={error} />}

          <div className="flex flex-wrap items-center gap-2">
            {offerSignIn && (
              // Loud only when the server asked for it; after a plain failure
              // it is one thing to try among others.
              <Button
                variant={server.status === "sign_in" ? "default" : "outline"}
                size="sm"
                className={ACTION}
                onClick={onSignIn}
                disabled={busy !== null}
              >
                <KeyRoundIcon className="size-3.5" />
                Sign in
              </Button>
            )}
            {kind === "http" && (
              <Button variant="outline" size="sm" className={ACTION} disabled={busy !== null} onClick={() => void run("check", "check_mcp_server")}>
                {busy === "check" ? <Spinner className="size-3.5" /> : <RefreshCwIcon className="size-3.5" />}
                Check
              </Button>
            )}
            {server.oauth && (
              <Button variant="outline" size="sm" className={ACTION} disabled={busy !== null} onClick={() => void run("signout", "sign_out_mcp_server")}>
                {busy === "signout" ? <Spinner className="size-3.5" /> : <LogOutIcon className="size-3.5" />}
                Sign out
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              className={ACTION}
              onClick={() => {
                setError("");
                setEditing(formFromServer(server));
              }}
            >
              <PencilIcon className="size-3.5" />
              Edit
            </Button>
            <Button
              variant="outline"
              size="sm"
              className={cn(ACTION, "text-destructive hover:text-destructive ml-auto")}
              onClick={() => setRemoving(true)}
            >
              <Trash2Icon className="size-3.5" />
              Remove
            </Button>
          </div>
        </div>

        {live && (
          <section aria-label="In this thread" className="space-y-1.5">
            <DetailHeading>In this thread</DetailHeading>
            <div className="space-y-1.5 rounded-lg border px-3 py-2">
              <div className="flex min-h-8 flex-wrap items-center gap-2">
                <MarkChip mark={liveMark(live.status)} />
                <span className="text-muted-foreground min-w-0 flex-1 text-[12.5px]">What the agent reports.</span>
                {canReconnect(live.status) && (
                  <Button variant="outline" size="sm" className={ACTION} disabled={reconnecting} onClick={onReconnect}>
                    {reconnecting ? <Spinner className="size-3.5" /> : <RotateCwIcon className="size-3.5" />}
                    Reconnect
                  </Button>
                )}
              </div>
              {reconnectError ? (
                <FoldedProblem problem={`Reconnect failed. ${reconnectError}`} tone="bad" />
              ) : (
                live.error && <FoldedProblem problem={live.error} tone={live.status === "failed" ? "bad" : "attention"} />
              )}
            </div>
          </section>
        )}

        <section aria-label="Agents that get it" className="space-y-1.5">
          <DetailHeading>Agents that get it</DetailHeading>
          <AgentSwitches harnesses={harnessesFor(harnesses, kind)} off={off} onChange={setAgents} />
          <p className="text-muted-foreground px-1 text-[12px] leading-snug">Takes effect in threads started after the change.</p>
        </section>

        <section aria-label="Details" className="space-y-1.5">
          <DetailHeading>Details</DetailHeading>
          <FactList
            facts={[
              ["URL", server.url],
              ["Command", server.command],
              ["Arguments", server.args?.length ? server.args.join(" ") : undefined],
              ["Headers", server.headerNames.length ? server.headerNames.join(", ") : undefined],
              ["Environment", server.envNames.length ? server.envNames.join(", ") : undefined],
            ]}
          />
        </section>
      </div>

      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={`Remove ${server.name}?`}
        description="Every agent loses it from the next thread on, and its stored sign-in and values are deleted."
        confirmLabel="Remove server"
        onConfirm={async () => {
          await command("remove_mcp_server", { name: server.name });
          onRemoved(server.name);
        }}
      />
    </div>
  );
}
