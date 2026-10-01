import { useCallback, useEffect, useRef, useState } from "react";

import { FlowDialog, type AuthWires } from "~/components/AuthFlowDialog";
import { SettingsPane } from "~/components/SettingsPane";
import { Button } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import {
  emptyServerForm,
  formFromDraft,
  formFromServer,
  upsert,
  type ServerForm,
} from "~/lib/connections";
import type { Cli, Connections, McpServer } from "~/protocol";

import { errorText } from "./parts";
import { AddServer, FoundList, ServerDetail, ServerFormView, ServerList } from "./Servers";
import { CliFormView, SignInList } from "./SignIns";

type View =
  | { kind: "list" }
  | { kind: "server"; name: string; justSaved?: boolean }
  | { kind: "paste" }
  | { kind: "new"; form: ServerForm }
  | { kind: "edit"; name: string }
  | { kind: "cli"; id: string | null };

type SignIn = { kind: "server"; name: string } | { kind: "account"; cli: Cli; account: string };

const byName = (s: McpServer) => s.name;
const byId = (c: Cli) => c.id;

/**
 * Settings → Connections: Omniplex's own MCP servers, the ones each agent
 * already has, and the command-line tools it signs in for. Fetched once on
 * open; every change answers with the changed entry, which is merged in
 * rather than fetching the whole list again.
 */
export function ConnectionsSettings({
  wires,
  server,
  onBack,
}: {
  wires: AuthWires;
  /** Open on this server's detail. */
  server?: string;
  onBack?: () => void;
}) {
  const { command } = wires;
  const [conn, setConn] = useState<Connections | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>(server ? { kind: "server", name: server } : { kind: "list" });
  const [signIn, setSignIn] = useState<SignIn | null>(null);
  const checkedOnce = useRef(false);

  const go = (v: View) => {
    setError(null);
    setView(v);
  };

  const putServer = useCallback((s: McpServer, previous?: string) => {
    setConn((c) => c && { ...c, servers: upsert(c.servers, s, byName, previous) });
  }, []);
  const putCli = useCallback((cli: Cli, previous?: string) => {
    setConn((c) => c && { ...c, clis: upsert(c.clis, cli, byId, previous) });
  }, []);

  const load = useCallback(() => {
    setLoadError(null);
    command("list_connections", {})
      .then((r: Connections) =>
        setConn({
          harnesses: r?.harnesses ?? [],
          servers: r?.servers ?? [],
          found: r?.found ?? [],
          clis: r?.clis ?? [],
        }),
      )
      .catch((e: unknown) => setLoadError(errorText(e)));
  }, [command]);

  useEffect(load, [load]);

  // The server never probes on a list, so after a restart everything reads
  // unchecked. Check those once, on open, so the chips mean something; after
  // that only the Check buttons ask again.
  useEffect(() => {
    if (!conn || checkedOnce.current) return;
    checkedOnce.current = true;
    for (const s of conn.servers) {
      if (s.url && s.status === "unchecked") {
        command("check_mcp_server", { name: s.name })
          .then((r: { server?: McpServer }) => r?.server && putServer(r.server))
          .catch(() => {});
      }
    }
    for (const c of conn.clis) {
      if (c.accounts.some((a) => a.status === "unchecked")) {
        command("check_cli", { id: c.id })
          .then((r: { cli?: Cli }) => r?.cli && putCli(r.cli))
          .catch(() => {});
      }
    }
  }, [conn, command, putServer, putCli]);

  // After a sign-in the flow has stored the credential; ask for the result.
  const afterSignIn = (s: SignIn) => {
    setSignIn(null);
    if (s.kind === "server") {
      command("check_mcp_server", { name: s.name })
        .then((r: { server?: McpServer }) => r?.server && putServer(r.server))
        .catch((e: unknown) => setError(errorText(e)));
    } else {
      command("check_cli", { id: s.cli.id })
        .then((r: { cli?: Cli }) => r?.cli && putCli(r.cli))
        .catch((e: unknown) => setError(errorText(e)));
    }
  };

  const back = view.kind === "list" ? onBack : () => go({ kind: "list" });

  let body: React.ReactNode;
  if (!conn) {
    body = loadError ? (
      <div className="space-y-2">
        <p className="text-destructive text-[12px] break-words">{loadError}</p>
        <Button variant="outline" size="sm" onClick={load}>
          Try again
        </Button>
      </div>
    ) : (
      <p className="text-muted-foreground flex items-center gap-2 text-[12px]">
        <Spinner aria-hidden className="size-3.5" />
        Loading…
      </p>
    );
  } else if (view.kind === "server" || view.kind === "edit") {
    const s = conn.servers.find((x) => x.name === view.name);
    if (!s) {
      body = (
        <div className="space-y-3">
          <p className="text-muted-foreground text-[12px]">This server no longer exists.</p>
          <Button variant="outline" size="sm" onClick={() => go({ kind: "list" })}>
            Back to list
          </Button>
        </div>
      );
    } else if (view.kind === "server") {
      body = (
        <ServerDetail
          key={s.name}
          server={s}
          harnesses={conn.harnesses}
          command={command}
          justSaved={view.justSaved}
          onSaved={putServer}
          onRemoved={() => {
            setConn((c) =>
              c && {
                ...c,
                servers: c.servers.filter((x) => x.name !== s.name),
                found: c.found.map((f) => (f.name === s.name ? { ...f, added: false } : f)),
              },
            );
            go({ kind: "list" });
          }}
          onEdit={() => go({ kind: "edit", name: s.name })}
          onSignIn={() => setSignIn({ kind: "server", name: s.name })}
          onError={setError}
        />
      );
    } else {
      body = (
        <ServerFormView
          key={s.name}
          initial={formFromServer(s)}
          harnesses={conn.harnesses}
          command={command}
          previousName={s.name}
          onSaved={(saved, previous) => {
            putServer(saved, previous);
            go({ kind: "server", name: saved.name, justSaved: true });
          }}
          onCancel={() => go({ kind: "server", name: s.name })}
        />
      );
    }
  } else if (view.kind === "paste") {
    body = (
      <AddServer
        command={command}
        onParsed={(draft) => go({ kind: "new", form: formFromDraft(draft) })}
        onByHand={() => go({ kind: "new", form: emptyServerForm() })}
      />
    );
  } else if (view.kind === "new") {
    body = (
      <ServerFormView
        initial={view.form}
        harnesses={conn.harnesses}
        command={command}
        onSaved={(saved) => {
          putServer(saved);
          go({ kind: "server", name: saved.name, justSaved: true });
        }}
        onCancel={() => go({ kind: "list" })}
      />
    );
  } else if (view.kind === "cli") {
    const existing = view.id ? conn.clis.find((c) => c.id === view.id) : undefined;
    body = (
      <CliFormView
        key={view.id ?? "new"}
        existing={existing}
        takenIds={conn.clis.map((c) => c.id)}
        command={command}
        onSaved={(cli, previous) => {
          putCli(cli, previous);
          go({ kind: "list" });
        }}
        onRemoved={(id) => {
          setConn((c) => c && { ...c, clis: c.clis.filter((x) => x.id !== id) });
          go({ kind: "list" });
        }}
        onCancel={() => go({ kind: "list" })}
      />
    );
  } else {
    body = (
      <>
        <ServerList
          servers={conn.servers}
          onOpen={(name) => go({ kind: "server", name })}
          onAdd={() => go({ kind: "paste" })}
        />
        <FoundList
          conn={conn}
          command={command}
          onAdded={(s, from) => {
            setConn((c) =>
              c && {
                ...c,
                servers: upsert(c.servers, s, byName),
                found: c.found.map((f) =>
                  f.harness === from.harness && f.name === from.name ? { ...f, added: true } : f,
                ),
              },
            );
          }}
          onError={setError}
        />
        <Separator />
        <SignInList
          clis={conn.clis}
          command={command}
          onCli={putCli}
          onEdit={(id) => go({ kind: "cli", id })}
          onAdd={() => go({ kind: "cli", id: null })}
          onSignIn={(cli, account) => setSignIn({ kind: "account", cli, account })}
          onError={setError}
        />
      </>
    );
  }

  return (
    <SettingsPane
      title="Connections"
      description="MCP servers every agent can use, and the tools Omniplex signs in for."
      onBack={back}
      error={error}
    >
      {body}
      {signIn && (
        <FlowDialog
          wires={wires}
          title={signIn.kind === "server" ? `Sign in to ${signIn.name}` : `Sign in ${signIn.account}`}
          description={
            signIn.kind === "account"
              ? `${signIn.cli.name}, account ${signIn.account}`
              : "Open the sign-in page, approve, and this closes by itself."
          }
          begin={
            signIn.kind === "server"
              ? { mcpServer: signIn.name, origin: window.location.origin }
              : { cli: signIn.cli.id, account: signIn.account }
          }
          onFinished={() => afterSignIn(signIn)}
          onClose={() => setSignIn(null)}
        />
      )}
    </SettingsPane>
  );
}
