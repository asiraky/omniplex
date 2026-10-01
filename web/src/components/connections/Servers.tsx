import {
  ChevronRightIcon,
  ClipboardPasteIcon,
  KeyRoundIcon,
  LogOutIcon,
  PencilIcon,
  RefreshCwIcon,
} from "lucide-react";
import { useId, useState } from "react";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import { Textarea } from "~/components/ui/textarea";
import {
  harnessesFor,
  joinNames,
  offersSignIn,
  serverKind,
  serverSaveArgs,
  serverWhere,
  toggleOff,
  type ServerForm,
} from "~/lib/connections";
import { formatAge } from "~/lib/usageFormat";
import { cn } from "~/lib/utils";
import type {
  Connections,
  FoundServer,
  McpDraft,
  McpHarness,
  McpServer,
  McpServerStatus,
  SetMcpServerOffArgs,
} from "~/protocol";

import { ConfirmRemove, errorText, RowsEditor, SectionHeading, StatusChip, type Tone } from "./parts";

export type Command = (command: string, args: unknown) => Promise<any>;

const CHIP: Record<McpServerStatus, { label: string; tone: Tone } | null> = {
  connected: { label: "Connected", tone: "good" },
  sign_in: { label: "Sign in", tone: "warn" },
  failed: { label: "Failed", tone: "bad" },
  unchecked: null,
};

export function ServerChip({ status }: { status: McpServerStatus }) {
  const chip = CHIP[status];
  return <StatusChip label={chip?.label} tone={chip?.tone} />;
}

/** The MCP servers list: a row per server, tap for its detail. */
export function ServerList({
  servers,
  onOpen,
  onAdd,
}: {
  servers: McpServer[];
  onOpen: (name: string) => void;
  onAdd: () => void;
}) {
  return (
    <div className="space-y-2">
      <SectionHeading
        action={
          <Button size="sm" variant="outline" onClick={onAdd}>
            Add server
          </Button>
        }
      >
        MCP servers
      </SectionHeading>
      <div className="overflow-hidden rounded-lg border">
        {servers.map((s, i) => (
          <button
            key={s.name}
            type="button"
            onClick={() => onOpen(s.name)}
            className={cn(
              "hover:bg-accent flex min-h-11 w-full items-center gap-2 px-3 py-2.5 text-left",
              i > 0 && "border-t",
            )}
          >
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5 text-[12px] font-medium">
                <span className="font-mono">{s.name}</span>
                <ServerChip status={s.status} />
              </span>
              <span className="text-muted-foreground block truncate font-mono text-[11px]">
                {serverWhere(s)}
              </span>
            </span>
            <ChevronRightIcon aria-hidden className="text-muted-foreground size-4 shrink-0" />
          </button>
        ))}
        {servers.length === 0 && (
          <p className="text-muted-foreground px-3 py-2.5 text-[11px]">
            No MCP servers yet. Add one and every agent gets it.
          </p>
        )}
      </div>
    </div>
  );
}

/** One switch per agent that runs this kind of server. */
function AgentSwitches({
  harnesses,
  off,
  disabled,
  onChange,
}: {
  harnesses: McpHarness[];
  off: string[];
  disabled?: boolean;
  onChange: (off: string[]) => void;
}) {
  const idBase = useId();
  if (harnesses.length === 0) {
    return <p className="text-muted-foreground text-[11px]">No agent here can use this server.</p>;
  }
  return (
    <div className="divide-y overflow-hidden rounded-lg border">
      {harnesses.map((h) => (
        <div key={h.id} className="flex min-h-11 items-center gap-2 px-3">
          <Label htmlFor={`${idBase}-${h.id}`} className="flex-1 text-[12px] font-normal">
            {h.name}
          </Label>
          <Switch
            id={`${idBase}-${h.id}`}
            checked={!off.includes(h.id)}
            disabled={disabled}
            onCheckedChange={(on) => onChange(toggleOff(off, h.id, on))}
          />
        </div>
      ))}
    </div>
  );
}

function statusLine(s: McpServer): string {
  if (serverKind(s) === "stdio" && s.status === "unchecked") {
    return "Omniplex starts this server only inside a thread. Its MCP tab shows how it is doing.";
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

/** A server's own screen: status, sign-in, which agents get it, edit, remove. */
export function ServerDetail({
  server,
  harnesses,
  command,
  justSaved,
  onSaved,
  onRemoved,
  onEdit,
  onSignIn,
  onError,
}: {
  server: McpServer;
  harnesses: McpHarness[];
  command: Command;
  /** Arrived here straight from saving it: offer the sign-in up front. */
  justSaved?: boolean;
  onSaved: (s: McpServer, previousName?: string) => void;
  onRemoved: () => void;
  onEdit: () => void;
  onSignIn: () => void;
  onError: (message: string | null) => void;
}) {
  const [busy, setBusy] = useState<"check" | "signout" | "agents" | null>(null);
  const kind = serverKind(server);

  const run = async (what: NonNullable<typeof busy>, cmd: string, args: unknown, previous?: string) => {
    setBusy(what);
    onError(null);
    try {
      const res = (await command(cmd, args)) as { server?: McpServer };
      if (res?.server) onSaved(res.server, previous);
    } catch (e) {
      onError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    onError(null);
    try {
      await command("remove_mcp_server", { name: server.name });
      onRemoved();
    } catch (e) {
      onError(errorText(e));
    }
  };

  return (
    <div className="space-y-5">
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="font-mono text-[13px] font-medium">{server.name}</h3>
          <ServerChip status={server.status} />
        </div>
        <p className="text-muted-foreground font-mono text-[11px] break-all">
          {server.url ?? serverWhere(server)}
        </p>
      </div>

      {justSaved && offersSignIn(server) && server.status === "sign_in" ? (
        <Alert>
          <AlertDescription className="flex flex-wrap items-center gap-2 text-[12px]">
            <span className="min-w-0 flex-1">Saved. This server wants you to sign in.</span>
            <Button size="sm" onClick={onSignIn}>
              <KeyRoundIcon />
              Sign in
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <div className="space-y-1">
          <p className="text-[12px]">{statusLine(server)}</p>
          {server.error && (
            <p className="text-muted-foreground text-[11px] break-words">{server.error}</p>
          )}
          {server.checkedAt && (
            <p className="text-muted-foreground text-[11px]">
              Checked {formatAge(Date.parse(server.checkedAt), Date.now())}
            </p>
          )}
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        {kind === "http" && (
          <Button
            variant="outline"
            size="sm"
            disabled={busy !== null}
            onClick={() => void run("check", "check_mcp_server", { name: server.name })}
          >
            {busy === "check" ? <Spinner aria-hidden className="size-3.5" /> : <RefreshCwIcon />}
            Check
          </Button>
        )}
        {offersSignIn(server) && (
          <Button size="sm" disabled={busy !== null} onClick={onSignIn}>
            <KeyRoundIcon />
            Sign in
          </Button>
        )}
        {server.oauth && (
          <Button
            variant="outline"
            size="sm"
            disabled={busy !== null}
            onClick={() => void run("signout", "sign_out_mcp_server", { name: server.name })}
          >
            {busy === "signout" ? <Spinner aria-hidden className="size-3.5" /> : <LogOutIcon />}
            Sign out
          </Button>
        )}
        <Button variant="outline" size="sm" disabled={busy !== null} onClick={onEdit}>
          <PencilIcon />
          Edit
        </Button>
      </div>

      <Separator />

      <div className="space-y-2">
        <SectionHeading>Agents that get it</SectionHeading>
        <AgentSwitches
          harnesses={harnessesFor(harnesses, kind)}
          off={server.off}
          disabled={busy !== null}
          onChange={(off) =>
            void run("agents", "set_mcp_server_off", { name: server.name, off } satisfies SetMcpServerOffArgs)
          }
        />
        <p className="text-muted-foreground text-[11px]">Takes effect in threads started after the change.</p>
      </div>

      <Separator />

      <ConfirmRemove
        label="Remove server"
        question={`Remove ${server.name} and its stored sign-in and values?`}
        onConfirm={remove}
      />
    </div>
  );
}

/** Paste first: most servers arrive as a URL, a command line or a JSON block. */
export function AddServer({
  command,
  onParsed,
  onByHand,
}: {
  command: Command;
  onParsed: (draft: McpDraft) => void;
  onByHand: () => void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const parse = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = (await command("parse_mcp_server", { text })) as { draft?: McpDraft };
      if (!res?.draft) throw new Error("Nothing in that looked like a server.");
      onParsed(res.draft);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (text.trim()) void parse();
      }}
    >
      <Label htmlFor="mcp-paste">Paste a URL, a claude mcp add command, or JSON</Label>
      <Textarea
        id="mcp-paste"
        autoFocus
        autoCapitalize="off"
        autoComplete="off"
        spellCheck={false}
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="https://mcp.example.com/mcp"
        className="max-h-60 min-h-24 font-mono md:text-[12px]"
      />
      {error && <p className="text-destructive text-[12px] break-words">{error}</p>}
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onByHand} className="mr-auto">
          Fill in by hand
        </Button>
        <Button type="submit" size="sm" disabled={busy || !text.trim()}>
          {busy ? <Spinner aria-hidden className="size-3.5" /> : <ClipboardPasteIcon />}
          Continue
        </Button>
      </div>
    </form>
  );
}

/** The server form, for a parsed paste, a blank start, or an edit. */
export function ServerFormView({
  initial,
  harnesses,
  command,
  previousName,
  onSaved,
  onCancel,
}: {
  initial: ServerForm;
  harnesses: McpHarness[];
  command: Command;
  /** Set when editing: the name it is stored under. */
  previousName?: string;
  onSaved: (s: McpServer, previousName?: string) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<ServerForm>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const idBase = useId();
  const set = (patch: Partial<ServerForm>) => setForm((f) => ({ ...f, ...patch }));
  const editing = previousName !== undefined;
  const hasStored = [...form.headers, ...form.env].some((r) => r.stored);

  const save = async () => {
    const built = serverSaveArgs(form, previousName);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = (await command("save_mcp_server", built.args)) as { server?: McpServer };
      if (!res?.server) throw new Error("The server did not answer with the saved server.");
      onSaved(res.server, previousName);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <div className="space-y-1.5">
        <Label htmlFor={`${idBase}-name`}>Name</Label>
        <Input
          id={`${idBase}-name`}
          autoCapitalize="off"
          autoComplete="off"
          spellCheck={false}
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
          placeholder="cloudflare"
          className="font-mono md:text-[12px]"
        />
      </div>

      <div className="space-y-2">
        <div role="radiogroup" aria-label="How it runs" className="bg-muted inline-flex rounded-lg p-0.5">
          {(
            [
              ["http", "URL"],
              ["stdio", "Command"],
            ] as const
          ).map(([kind, label]) => (
            <button
              key={kind}
              type="button"
              role="radio"
              aria-checked={form.kind === kind}
              onClick={() => set({ kind })}
              className={cn(
                "min-h-9 rounded-md px-3 text-[12px] font-medium",
                form.kind === kind ? "bg-background shadow-xs" : "text-muted-foreground",
              )}
            >
              {label}
            </button>
          ))}
        </div>

        {form.kind === "http" ? (
          <div className="space-y-1.5">
            <Label htmlFor={`${idBase}-url`}>URL</Label>
            <Input
              id={`${idBase}-url`}
              type="url"
              inputMode="url"
              autoCapitalize="off"
              autoComplete="off"
              spellCheck={false}
              value={form.url}
              onChange={(e) => set({ url: e.target.value })}
              placeholder="https://mcp.example.com/mcp"
              className="font-mono md:text-[12px]"
            />
          </div>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label htmlFor={`${idBase}-cmd`}>Command</Label>
              <Input
                id={`${idBase}-cmd`}
                autoCapitalize="off"
                autoComplete="off"
                spellCheck={false}
                value={form.command}
                onChange={(e) => set({ command: e.target.value })}
                placeholder="npx"
                className="font-mono md:text-[12px]"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor={`${idBase}-args`}>Arguments, one per line</Label>
              <Textarea
                id={`${idBase}-args`}
                autoCapitalize="off"
                autoComplete="off"
                spellCheck={false}
                value={form.args}
                onChange={(e) => set({ args: e.target.value })}
                placeholder={"-y\n@example/mcp-server"}
                className="max-h-48 min-h-16 font-mono md:text-[12px]"
              />
            </div>
          </>
        )}
      </div>

      {form.kind === "http" ? (
        <RowsEditor
          label="Headers"
          secret
          rows={form.headers}
          onChange={(headers) => set({ headers })}
          namePlaceholder="X-Api-Key"
          valuePlaceholder="Value"
        />
      ) : (
        <RowsEditor
          label="Environment"
          secret
          rows={form.env}
          onChange={(env) => set({ env })}
          namePlaceholder="API_KEY"
          valuePlaceholder="Value"
        />
      )}
      {hasStored && (
        <p className="text-muted-foreground -mt-3 text-[11px]">
          Stored values are never shown. Leave one blank to keep it; type to replace it.
        </p>
      )}

      <div className="space-y-2">
        <SectionHeading>Agents that get it</SectionHeading>
        <AgentSwitches
          harnesses={harnessesFor(harnesses, form.kind)}
          off={form.off}
          onChange={(off) => set({ off })}
        />
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
        </Alert>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? <Spinner aria-hidden className="size-4" /> : null}
          {editing ? "Save" : "Add server"}
        </Button>
      </div>
    </form>
  );
}

/** Servers each agent already has in its own config, folded away by default. */
export function FoundList({
  conn,
  command,
  onAdded,
  onError,
}: {
  conn: Connections;
  command: Command;
  onAdded: (s: McpServer, from: FoundServer) => void;
  onError: (message: string | null) => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  if (conn.found.length === 0) return null;
  const agentName = (id: string) => conn.harnesses.find((h) => h.id === id)?.name ?? id;
  const sources = joinNames([...new Set(conn.found.map((f) => agentName(f.harness)))]);

  const add = async (f: FoundServer) => {
    const key = `${f.harness}:${f.name}`;
    setBusy(key);
    onError(null);
    try {
      const res = (await command("add_found_server", { harness: f.harness, name: f.name })) as {
        server?: McpServer;
      };
      if (res?.server) onAdded(res.server, f);
    } catch (e) {
      onError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <Collapsible>
      <CollapsibleTrigger className="hover:text-foreground focus-visible:ring-ring group flex min-h-11 w-full items-center gap-1.5 rounded-md text-left text-[12px] font-medium outline-none focus-visible:ring-2 md:min-h-8">
        <ChevronRightIcon className="text-muted-foreground size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
        <span className="min-w-0 flex-1">Found in {sources}</span>
        <span className="text-muted-foreground tabular-nums">{conn.found.length}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="mt-1 overflow-hidden rounded-lg border">
          {conn.found.map((f, i) => {
            const key = `${f.harness}:${f.name}:${f.url ?? f.command ?? ""}`;
            return (
              <div key={key} className={cn("flex items-center gap-2 px-3 py-2", i > 0 && "border-t")}>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-[12px]">{f.name}</p>
                  <p className="text-muted-foreground truncate text-[11px]">
                    {agentName(f.harness)} · {f.origin}
                  </p>
                  <p className="text-muted-foreground truncate font-mono text-[11px]">{serverWhere(f)}</p>
                </div>
                {f.added ? (
                  <span className="text-muted-foreground shrink-0 text-[11px]">Added</span>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    disabled={busy !== null}
                    onClick={() => void add(f)}
                  >
                    {busy === `${f.harness}:${f.name}` ? <Spinner aria-hidden className="size-3.5" /> : null}
                    Add to Omniplex
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
