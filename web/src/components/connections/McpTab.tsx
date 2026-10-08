import { KeyRoundIcon, PlusIcon, RefreshCwIcon, RotateCwIcon } from "lucide-react";
import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { FlowDialog, type AuthWires } from "~/components/AuthFlowDialog";
import { IconButton } from "~/components/IconButton";
import {
  DetailHeader,
  ErrorLine,
  FactList,
  FoldedProblem,
  ListRow,
  ListToolbar,
  Loading,
  LoadError,
  Marker,
  RowSwitch,
  Section,
  type PageCommand,
} from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import {
  foundByHarness,
  foundWhere,
  groupServers,
  joinNames,
  liveAction,
  liveSource,
  liveMark,
  offSummary,
  onInProject,
  projectLabel,
  sameHost,
  serverKey,
  serverMark,
  serverMatches,
  serverRef,
  serverWhere,
  shadowing,
  sortLive,
  threadServers,
} from "~/lib/connections";
import { cn, errorText } from "~/lib/utils";
import type { FoundServer, McpServer, ThreadMcp } from "~/protocol";

import { AddServerSheet } from "./AddServerSheet";
import { MarkChip } from "./parts";
import { ServerDetail } from "./ServerDetail";
import { useLiveReport, useProjectOff, type ConnectionsStore, type LiveReport } from "./useConnections";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";

const foundKey = (f: FoundServer) => `${f.project ?? ""}:${f.harness}:${f.name}:${foundWhere(f)}`;

type Open = { kind: "server"; key: string } | { kind: "found"; key: string };

type Ref = { name: string; project?: string };

const NO_PROJECTS: { id: string; name: string }[] = [];

/** Why a row is not what the thread gets, said on the row: short. */
const SHADOW_SUB = {
  replaces: "Used here instead of the Everywhere one",
  replaced: "This project uses its own instead",
} as const;

/**
 * The MCP tab: what the open thread's agent reports, Omniplex's own servers,
 * and what each agent already has in its own config. In a project the servers
 * come in two groups, the project's own and the ones everywhere, each of
 * those with a switch for this project. Outside one, every project's servers
 * are there too, folded under its name.
 */
export function McpTab({
  wires,
  store,
  threadId,
  projectId,
  projects = NO_PROJECTS,
  shown,
}: {
  wires: AuthWires;
  store: ConnectionsStore;
  /** The open thread, whose running session can be asked. */
  threadId?: string;
  /** The project in view: the thread's, or the one being worked in. */
  projectId?: string;
  projects?: { id: string; name: string }[];
  shown: boolean;
}) {
  const command = wires.command as PageCommand;
  const { conn } = store;
  const live = useLiveReport(command, threadId, shown);
  const projectOff = useProjectOff(command, store.putServer);
  const nameOf = (id: string) => projectLabel(id, projects);

  const [query, setQuery] = useState("");
  const [folds, setFolds] = useState<Record<string, boolean>>({});
  const [searchFolds, setSearchFolds] = useState<Record<string, boolean>>({});
  const [open, setOpen] = useState<Open | null>(null);
  const [adding, setAdding] = useState(false);
  const [addSeq, setAddSeq] = useState(0);
  const [signIn, setSignIn] = useState<Ref | null>(null);
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

  // What the open thread's session gets from us, where its names resolve.
  const ours = useMemo(() => (conn ? threadServers(conn.servers, projectId) : null), [conn, projectId]);
  const oursNamed = (name: string) => ours?.find((s) => s.name === name);
  const liveRows = useMemo(
    () => sortLive((live.report?.servers ?? []).filter((s) => serverMatches(s, query))),
    [live.report, query],
  );
  const groups = useMemo(
    () => groupServers((conn?.servers ?? []).filter((s) => serverMatches(s, query)), projectId),
    [conn, query, projectId],
  );
  const projectGroups = [...groups.projects].sort((a, b) =>
    nameOf(a.project).toLowerCase().localeCompare(nameOf(b.project).toLowerCase()),
  );
  const listed = groups.here.length + groups.everywhere.length + groups.projects.length;
  const found = useMemo(
    () => foundByHarness((conn?.found ?? []).filter((f) => serverMatches(f, query)), conn?.harnesses ?? []),
    [conn, query],
  );

  // After a sign-in the flow has stored the credential: read the result, and
  // have the open thread's session try the server again.
  const afterSignIn = async (target: Ref) => {
    setSignIn(null);
    setAfterError("");
    try {
      const r = await command<{ server?: McpServer }>("check_mcp_server", serverRef(target));
      if (r?.server) store.putServer(r.server);
    } catch (e) {
      setAfterError(errorText(e));
    }
    // Only when the server signed in to is the one the thread has by that name.
    const mine = oursNamed(target.name);
    if (
      mine &&
      serverKey(mine) === serverKey(target) &&
      live.report?.live &&
      live.report.servers.some((s) => s.name === target.name)
    ) {
      await live.reconnect(target.name, target.project);
    }
  };

  const openServer = (s: Ref) => go({ kind: "server", key: serverKey(s) });
  const fromThread = (name: string, then: (s: McpServer) => void) => {
    const s = oursNamed(name);
    if (s) then(s);
  };

  const liveSection = threadId && (!searching || liveRows.length > 0) && (
    <LiveSection
      live={live}
      rows={liveRows}
      ours={ours}
      searching={searching}
      open={sectionOpen("live", true)}
      onOpenChange={(next) => setSectionOpen("live", next)}
      onOpenServer={(name) => fromThread(name, openServer)}
      onSignIn={(name) => fromThread(name, (s) => setSignIn(serverRef(s)))}
      onReconnect={(name) => fromThread(name, (s) => void live.reconnect(s.name, s.project))}
    />
  );

  let rest: ReactNode = null;
  if (conn) {
    const row = (s: McpServer) => {
      const only = offSummary(s, conn.harnesses);
      const shadow = shadowing(s, conn.servers, projectId);
      const switchable = !!projectId && !s.project;
      const on = !switchable || onInProject(s, projectId);
      return (
        <li key={serverKey(s)}>
          <ListRow
            title={s.name}
            markers={
              <>
                {only && <Marker>{only}</Marker>}
                <MarkChip mark={serverMark(s)} />
              </>
            }
            sub={shadow ? `${SHADOW_SUB[shadow]} · ${serverWhere(s)}` : serverWhere(s)}
            dim={only === "Off" || !on || shadow === "replaced"}
            onOpen={() => openServer(s)}
            action={
              switchable && (
                <RowSwitch
                  label={`${s.name} in this project`}
                  checked={on}
                  onCheckedChange={(next) => void projectOff.set(s, projectId, next)}
                />
              )
            }
          />
        </li>
      );
    };
    const group = (key: string, title: string, list: McpServer[], byDefault: boolean, note?: ReactNode) =>
      (!searching || list.length > 0) && (
        <Section
          key={key}
          title={title}
          count={list.length}
          open={sectionOpen(key, byDefault)}
          onOpenChange={(next) => setSectionOpen(key, next)}
          note={note}
        >
          {list.length > 0 && <ul>{list.map(row)}</ul>}
        </Section>
      );
    const empty = !searching && conn.servers.length === 0;
    rest = (
      <>
        {projectId ? (
          <>
            {group(
              "here",
              "This project",
              groups.here,
              true,
              !searching && groups.here.length === 0
                ? "None yet. Add one here and only this project's threads get it."
                : undefined,
            )}
            {group(
              "everywhere",
              "Everywhere",
              groups.everywhere,
              true,
              searching
                ? undefined
                : groups.everywhere.length === 0
                  ? "None yet. Add one everywhere and every project gets it."
                  : "Switch one off to keep it out of this project.",
            )}
          </>
        ) : (
          <>
            {group(
              "everywhere",
              "Everywhere",
              groups.everywhere,
              true,
              empty ? "No MCP servers yet. Add one and every agent gets it." : undefined,
            )}
            {projectGroups.map((g) => group(`project:${g.project}`, nameOf(g.project), g.servers, false))}
          </>
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
                    markers={
                      <>
                        {f.project && <Marker>This project</Marker>}
                        {f.added && <Marker>Added</Marker>}
                      </>
                    }
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
    searching && conn !== null && liveRows.length === 0 && listed === 0 && found.length === 0;

  let detail: ReactNode = null;
  if (open?.kind === "server" && conn) {
    const s = conn.servers.find((x) => serverKey(x) === open.key);
    // The thread's report on a name is about this server only if it is the
    // one the thread gets by that name.
    const inThread = !!s && !!ours?.some((o) => serverKey(o) === serverKey(s));
    const shadow = s ? shadowing(s, conn.servers, projectId) : null;
    detail = s ? (
      <ServerDetail
        key={serverKey(s)}
        server={s}
        scope={s.project ? nameOf(s.project) : "Everywhere"}
        harnesses={conn.harnesses}
        command={command}
        project={projectId && !s.project ? { id: projectId, name: nameOf(projectId) } : undefined}
        shadow={shadow}
        live={inThread && live.report?.live ? live.report.servers.find((x) => x.name === s.name) : undefined}
        reconnecting={inThread && live.busy === s.name}
        reconnectError={inThread && live.failed?.name === s.name ? live.failed.error : undefined}
        onReconnect={() => void live.reconnect(s.name, s.project)}
        onBack={() => go(null)}
        onSaved={(saved, previous) => {
          store.putServer(saved, previous);
          if (saved.name !== s.name) setOpen({ kind: "server", key: serverKey(saved) });
        }}
        onRemoved={(gone) => {
          store.removeServer(gone);
          go(null);
        }}
        onSignIn={() => setSignIn(serverRef(s))}
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
        projectName={f.project ? nameOf(f.project) : undefined}
        ours={conn.servers}
        command={command}
        onBack={() => go(null)}
        onAdded={(s) => store.foundAdded(f, s)}
        onOpenServer={openServer}
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
          {projectOff.error && <ErrorLine message={projectOff.error} className="px-2 py-2" />}
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
          projectId={projectId}
          projects={projects}
          onSaved={(s) => {
            store.putServer(s);
            openServer(s);
          }}
        />
      )}

      {signIn && (
        <FlowDialog
          wires={wires}
          title={`Sign in to ${signIn.name}`}
          description={signInNote(signIn, conn?.servers ?? [], nameOf)}
          begin={{
            mcpServer: signIn.name,
            ...(signIn.project ? { mcpProject: signIn.project } : {}),
            origin: window.location.origin,
          }}
          onFinished={() => void afterSignIn(signIn)}
          onClose={() => setSignIn(null)}
        />
      )}
    </div>
  );
}

function signInNote(target: Ref, servers: McpServer[], nameOf: (id: string) => string): string {
  const s = servers.find((x) => serverKey(x) === serverKey(target));
  const others = s ? sameHost(s, servers) : [];
  if (others.length === 0) return "Open the sign-in page and approve. This closes by itself.";
  // Two of the same name are told apart by where they apply.
  const label = (o: Ref) =>
    o.name === target.name || others.some((x) => x !== o && x.name === o.name)
      ? `${o.name} (${o.project ? nameOf(o.project) : "Everywhere"})`
      : o.name;
  return `${joinNames(others.map(label))} ${others.length === 1 ? "uses" : "use"} the same host, so sign in with the login you want for ${label(target)}. If the page skips the login and signs you in as someone else, tap Copy URL and open it in a private window. This closes by itself.`;
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
  onReconnect,
}: {
  live: LiveReport;
  rows: ThreadMcp[];
  /** The thread's own set from us (threadServers). */
  ours: McpServer[] | null;
  searching: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenServer: (name: string) => void;
  onSignIn: (name: string) => void;
  onReconnect: (name: string) => void;
}) {
  const { report } = live;
  let note: ReactNode;
  if (live.error) note = <FoldedProblem problem={`Could not ask the agent. ${live.error}`} tone="bad" />;
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
            const source = ours ? liveSource(s.name, ours) : null;
            const own = source === "ours";
            const failed = live.failed?.name === s.name ? `Reconnect failed. ${live.failed.error}` : undefined;
            return (
              <li key={s.name}>
                <ListRow
                  title={s.name}
                  markers={<MarkChip mark={liveMark(s.status)} />}
                  sub={
                    source === "built_in"
                      ? "Built into Omniplex"
                      : source === "theirs"
                        ? action === "theirs"
                          ? "From the agent's own config. Fix it there."
                          : "From the agent's own config"
                        : undefined
                  }
                  problem={failed ?? s.error}
                  foldProblem
                  problemTone={failed || s.status === "failed" ? "bad" : "attention"}
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
                        onClick={() => onReconnect(s.name)}
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
  projectName,
  ours,
  command,
  onBack,
  onAdded,
  onOpenServer,
}: {
  found: FoundServer;
  agent: string;
  /** Found in this project's folders: it is added to the project. */
  projectName?: string;
  ours: McpServer[];
  command: PageCommand;
  onBack: () => void;
  onAdded: (s: McpServer) => void;
  onOpenServer: (s: McpServer) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [addedAs, setAddedAs] = useState<string | null>(null);
  const mineKey = serverKey({ name: addedAs ?? found.name, project: found.project });
  const mine = ours.find((s) => serverKey(s) === mineKey);
  const gets = projectName ? `every agent gets it in ${projectName}'s threads` : "every agent gets it";

  const add = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>("add_found_server", {
        harness: found.harness,
        name: found.name,
        where: foundWhere(found),
        ...(found.project ? { project: found.project } : {}),
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
            `Omniplex has this server too, so ${gets}.`
          ) : (
            <>
              Only {agent} has this. Add it to Omniplex and {gets}.{" "}
              <span className="text-muted-foreground">{agent}'s own copy stays as it is.</span>
            </>
          )}
        </p>
        {error && <ErrorLine message={error} />}
        <div className="flex flex-wrap gap-2">
          {found.added || addedAs ? (
            mine && (
              <Button variant="outline" size="sm" className={ACTION} onClick={() => onOpenServer(mine)}>
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
