import { KeyRoundIcon, PlusIcon, RefreshCwIcon, RotateCwIcon } from "lucide-react";
import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { FlowDialog, type AuthWires } from "~/components/AuthFlowDialog";
import { IconButton } from "~/components/IconButton";
import {
  DetailHeader,
  ErrorLine,
  FactList,
  ListRow,
  ListToolbar,
  Loading,
  LoadError,
  Marker,
  Section,
  type PageCommand,
} from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import {
  foundByHarness,
  foundWhere,
  liveAction,
  liveMark,
  offSummary,
  serverMark,
  serverMatches,
  serverWhere,
  sortLive,
} from "~/lib/connections";
import { cn, errorText } from "~/lib/utils";
import type { FoundServer, McpServer, ThreadMcp } from "~/protocol";

import { AddServerSheet } from "./AddServerSheet";
import { MarkChip } from "./parts";
import { ServerDetail } from "./ServerDetail";
import { useLiveReport, type ConnectionsStore, type LiveReport } from "./useConnections";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";

const foundKey = (f: FoundServer) => `${f.harness}:${f.name}:${foundWhere(f)}`;

type Open = { kind: "server"; name: string } | { kind: "found"; key: string };

/**
 * The MCP tab: what the open thread's agent reports, Omniplex's own servers,
 * and what each agent already has in its own config.
 */
export function McpTab({
  wires,
  store,
  threadId,
  shown,
}: {
  wires: AuthWires;
  store: ConnectionsStore;
  /** The open thread, whose running session can be asked. */
  threadId?: string;
  shown: boolean;
}) {
  const command = wires.command as PageCommand;
  const { conn } = store;
  const live = useLiveReport(command, threadId, shown);

  const [query, setQuery] = useState("");
  const [folds, setFolds] = useState<Record<string, boolean>>({});
  const [searchFolds, setSearchFolds] = useState<Record<string, boolean>>({});
  const [open, setOpen] = useState<Open | null>(null);
  const [adding, setAdding] = useState(false);
  const [addSeq, setAddSeq] = useState(0);
  const [signIn, setSignIn] = useState<string | null>(null);
  const [afterError, setAfterError] = useState("");

  const listScrollRef = useRef<HTMLDivElement>(null);
  const savedScroll = useRef(0);

  const go = (next: Open | null) => {
    if (!open && next) savedScroll.current = listScrollRef.current?.scrollTop ?? 0;
    setOpen(next);
  };

  useLayoutEffect(() => {
    if (!open && listScrollRef.current) listScrollRef.current.scrollTop = savedScroll.current;
  }, [open]);

  const searching = query.trim() !== "";
  const changeQuery = (next: string) => {
    setQuery(next);
    setSearchFolds({});
  };
  const sectionOpen = (key: string, byDefault: boolean) =>
    searching ? (searchFolds[key] ?? true) : (folds[key] ?? byDefault);
  const setSectionOpen = (key: string, next: boolean) =>
    (searching ? setSearchFolds : setFolds)((f) => ({ ...f, [key]: next }));

  const ours = conn?.servers ?? null;
  const liveRows = useMemo(
    () => sortLive((live.report?.servers ?? []).filter((s) => serverMatches(s, query))),
    [live.report, query],
  );
  const servers = useMemo(
    () =>
      (conn?.servers ?? [])
        .filter((s) => serverMatches(s, query))
        .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())),
    [conn, query],
  );
  const found = useMemo(
    () => foundByHarness((conn?.found ?? []).filter((f) => serverMatches(f, query)), conn?.harnesses ?? []),
    [conn, query],
  );

  // After a sign-in the flow has stored the credential: read the result, and
  // have the open thread's session try the server again.
  const afterSignIn = async (name: string) => {
    setSignIn(null);
    setAfterError("");
    try {
      const r = await command<{ server?: McpServer }>("check_mcp_server", { name });
      if (r?.server) store.putServer(r.server);
    } catch (e) {
      setAfterError(errorText(e));
    }
    if (live.report?.live && live.report.servers.some((s) => s.name === name)) await live.reconnect(name);
  };

  const liveSection = threadId && (!searching || liveRows.length > 0) && (
    <LiveSection
      live={live}
      rows={liveRows}
      ours={ours}
      searching={searching}
      open={sectionOpen("live", true)}
      onOpenChange={(next) => setSectionOpen("live", next)}
      onOpenServer={(name) => go({ kind: "server", name })}
      onSignIn={setSignIn}
    />
  );

  let rest: ReactNode = null;
  if (conn) {
    rest = (
      <>
        {(!searching || servers.length > 0) && (
          <Section
            title="Yours"
            count={servers.length}
            open={sectionOpen("yours", true)}
            onOpenChange={(next) => setSectionOpen("yours", next)}
            note={!searching && servers.length === 0 ? "No MCP servers yet. Add one and every agent gets it." : undefined}
          >
            {servers.length > 0 && (
              <ul>
                {servers.map((s) => {
                  const only = offSummary(s, conn.harnesses);
                  return (
                    <li key={s.name}>
                      <ListRow
                        title={s.name}
                        markers={
                          <>
                            {only && <Marker>{only}</Marker>}
                            <MarkChip mark={serverMark(s)} />
                          </>
                        }
                        sub={serverWhere(s)}
                        dim={only === "Off"}
                        onOpen={() => go({ kind: "server", name: s.name })}
                      />
                    </li>
                  );
                })}
              </ul>
            )}
          </Section>
        )}
        {found.map(({ harness, servers: list }) => (
          <Section
            key={harness.id}
            title={`Found in ${harness.name}`}
            count={list.length}
            open={sectionOpen(`found:${harness.id}`, false)}
            onOpenChange={(next) => setSectionOpen(`found:${harness.id}`, next)}
          >
            <ul>
              {list.map((f) => (
                <li key={foundKey(f)}>
                  <ListRow
                    title={f.name}
                    markers={f.added ? <Marker>Added</Marker> : undefined}
                    sub={serverWhere(f)}
                    onOpen={() => go({ kind: "found", key: foundKey(f) })}
                  />
                </li>
              ))}
            </ul>
          </Section>
        ))}
      </>
    );
  }

  const noMatches =
    searching && conn !== null && liveRows.length === 0 && servers.length === 0 && found.length === 0;

  let detail: ReactNode = null;
  if (open?.kind === "server" && conn) {
    const s = conn.servers.find((x) => x.name === open.name);
    detail = s ? (
      <ServerDetail
        key={s.name}
        server={s}
        harnesses={conn.harnesses}
        command={command}
        live={live.report?.live ? live.report.servers.find((x) => x.name === s.name) : undefined}
        reconnecting={live.busy === s.name}
        onReconnect={() => void live.reconnect(s.name)}
        onBack={() => go(null)}
        onSaved={(saved, previous) => {
          store.putServer(saved, previous);
          if (saved.name !== s.name) setOpen({ kind: "server", name: saved.name });
        }}
        onRemoved={(name) => {
          store.removeServer(name);
          go(null);
        }}
        onSignIn={() => setSignIn(s.name)}
      />
    ) : (
      <Gone onBack={() => go(null)} />
    );
  } else if (open?.kind === "found" && conn) {
    const f = conn.found.find((x) => foundKey(x) === open.key);
    detail = f ? (
      <FoundDetail
        found={f}
        agent={conn.harnesses.find((h) => h.id === f.harness)?.name ?? f.harness}
        ours={conn.servers}
        command={command}
        onBack={() => go(null)}
        onAdded={(s) => store.foundAdded(f, s)}
        onOpenServer={(name) => go({ kind: "server", name })}
      />
    ) : (
      <Gone onBack={() => go(null)} />
    );
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
        <ListToolbar
          query={query}
          onQuery={changeQuery}
          searchLabel="Search MCP servers"
          onAdd={() => {
            setAddSeq((n) => n + 1);
            setAdding(true);
          }}
        />
        <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
          {afterError && <ErrorLine message={afterError} className="px-2 py-2" />}
          {liveSection}
          {store.error && !conn && (
            <LoadError message={`Could not list MCP servers. ${store.error}`} onRetry={store.reload} busy={store.loading} />
          )}
          {store.loading && !conn && <Loading>Reading MCP servers…</Loading>}
          {noMatches && (
            <p className="text-muted-foreground px-2 py-10 text-center text-[12.5px]">
              No MCP servers match “{query.trim()}”.
            </p>
          )}
          {rest}
        </div>
      </div>

      {detail && <div className="absolute inset-0">{detail}</div>}

      {conn && (
        <AddServerSheet
          key={addSeq}
          open={adding}
          onOpenChange={setAdding}
          command={command}
          harnesses={conn.harnesses}
          onSaved={(s) => {
            store.putServer(s);
            go({ kind: "server", name: s.name });
          }}
        />
      )}

      {signIn && (
        <FlowDialog
          wires={wires}
          title={`Sign in to ${signIn}`}
          description="Open the sign-in page and approve. This closes by itself."
          begin={{ mcpServer: signIn, origin: window.location.origin }}
          onFinished={() => void afterSignIn(signIn)}
          onClose={() => setSignIn(null)}
        />
      )}
    </div>
  );
}

function Gone({ onBack }: { onBack: () => void }) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <DetailHeader backLabel="Back to MCP servers" onBack={onBack} title="Not found" mono={false} />
      <p className="text-muted-foreground px-4 py-10 text-center text-[12.5px]">This server is no longer here.</p>
    </div>
  );
}

/** The open thread's session, asked how each server is doing. */
function LiveSection({
  live,
  rows,
  ours,
  searching,
  open,
  onOpenChange,
  onOpenServer,
  onSignIn,
}: {
  live: LiveReport;
  rows: ThreadMcp[];
  ours: McpServer[] | null;
  searching: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenServer: (name: string) => void;
  onSignIn: (name: string) => void;
}) {
  const { report } = live;
  let note: ReactNode;
  if (live.error) note = <ErrorLine message={`Could not ask the agent. ${live.error}`} />;
  else if (!report && live.loading)
    note = (
      <span className="flex items-center gap-2">
        <Spinner className="text-primary size-3.5" /> Asking the agent…
      </span>
    );
  else if (report && !report.live) note = "No running session. Send a message and the agent starts with its servers.";
  else if (report && report.servers.length === 0) note = "This session has no MCP servers.";
  else if (!searching && report && report.servers.every((s) => s.status === "connected")) note = "All connected.";

  return (
    <Section
      title="This thread"
      count={report?.live ? rows.length : 0}
      open={open}
      onOpenChange={onOpenChange}
      note={note}
      action={
        <IconButton label="Ask the agent again" onClick={live.refresh} disabled={live.loading}>
          <RefreshCwIcon className={cn(live.loading && "animate-spin")} />
        </IconButton>
      }
    >
      {report?.live && rows.length > 0 && (
        <ul>
          {rows.map((s) => {
            const action = ours ? liveAction(s, ours) : null;
            const own = !!ours?.some((o) => o.name === s.name);
            return (
              <li key={s.name}>
                <ListRow
                  title={s.name}
                  markers={<MarkChip mark={liveMark(s.status)} />}
                  sub={
                    ours && !own
                      ? action === "theirs"
                        ? "From the agent's own config. Fix it there."
                        : "From the agent's own config"
                      : undefined
                  }
                  problem={s.error}
                  foldProblem
                  problemTone={s.status === "failed" ? "bad" : "attention"}
                  onOpen={own ? () => onOpenServer(s.name) : undefined}
                  action={
                    action === "sign_in" ? (
                      <Button size="sm" className={ACTION} onClick={() => onSignIn(s.name)}>
                        <KeyRoundIcon className="size-3.5" />
                        Sign in
                      </Button>
                    ) : action === "reconnect" ? (
                      <Button
                        variant="outline"
                        size="sm"
                        className={ACTION}
                        disabled={live.busy !== null}
                        onClick={() => void live.reconnect(s.name)}
                      >
                        {live.busy === s.name ? <Spinner className="size-3.5" /> : <RotateCwIcon className="size-3.5" />}
                        Reconnect
                      </Button>
                    ) : undefined
                  }
                />
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

/** A server from one agent's own config: what it is, and taking it over. */
function FoundDetail({
  found,
  agent,
  ours,
  command,
  onBack,
  onAdded,
  onOpenServer,
}: {
  found: FoundServer;
  agent: string;
  ours: McpServer[];
  command: PageCommand;
  onBack: () => void;
  onAdded: (s: McpServer) => void;
  onOpenServer: (name: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [addedAs, setAddedAs] = useState<string | null>(null);
  const mine = ours.find((s) => s.name === (addedAs ?? found.name));

  const add = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>("add_found_server", {
        harness: found.harness,
        name: found.name,
        where: foundWhere(found),
      });
      if (res?.server) {
        setAddedAs(res.server.name);
        onAdded(res.server);
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DetailHeader backLabel="Back to MCP servers" onBack={onBack} title={found.name} sub={`In ${agent}'s own config`} />
      <div className="scroll-thin min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain px-3 py-3">
        <p className="px-1 text-[13px] leading-snug">
          {found.added || addedAs ? (
            "Omniplex has this server too, so every agent gets it."
          ) : (
            <>
              Only {agent} has this. Add it to Omniplex and every agent gets it.{" "}
              <span className="text-muted-foreground">{agent}'s own copy stays as it is.</span>
            </>
          )}
        </p>
        {error && <ErrorLine message={error} />}
        <div className="flex flex-wrap gap-2">
          {found.added || addedAs ? (
            mine && (
              <Button variant="outline" size="sm" className={ACTION} onClick={() => onOpenServer(mine.name)}>
                Open {mine.name}
              </Button>
            )
          ) : (
            <Button size="sm" className={ACTION} onClick={() => void add()} disabled={busy}>
              {busy ? <Spinner className="size-3.5" /> : <PlusIcon className="size-3.5" />}
              Add to Omniplex
            </Button>
          )}
        </div>
        <FactList
          facts={[
            ["Where", found.origin],
            ["URL", found.url],
            ["Command", found.command],
            ["Arguments", found.args?.length ? found.args.join(" ") : undefined],
          ]}
        />
      </div>
    </div>
  );
}
