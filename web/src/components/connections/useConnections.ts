import { useCallback, useEffect, useRef, useState } from "react";

import type { PageCommand } from "~/components/tools/parts";
import { upsert } from "~/lib/connections";
import { errorText } from "~/lib/utils";
import type { Cli, Connections, FoundServer, McpServer, ThreadMcpReport } from "~/protocol";
import { useLatest } from "~/useLatest";

const byName = (s: McpServer) => s.name;
const byId = (c: Cli) => c.id;

export interface ConnectionsStore {
  conn: Connections | null;
  error: string;
  loading: boolean;
  reload: () => void;
  putServer: (s: McpServer, previousName?: string) => void;
  removeServer: (name: string) => void;
  /** A found server was added as `s`. */
  foundAdded: (from: FoundServer, s: McpServer) => void;
  putCli: (cli: Cli, previousId?: string) => void;
  removeCli: (id: string) => void;
}

/**
 * Omniplex's MCP servers, what each agent already has, and the sign-ins: read
 * once, the first time a tab that shows them is opened. Every change answers
 * with the changed entry, which is merged in rather than reading it all again.
 */
export function useConnections(command: PageCommand, wanted: boolean): ConnectionsStore {
  const [conn, setConn] = useState<Connections | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [seq, setSeq] = useState(0);
  const commandRef = useLatest(command);
  const checked = useRef(false);
  // Latches on: hiding the tab again must not read the list again.
  const [on, setOn] = useState(wanted);
  if (wanted && !on) setOn(true);

  const putServer = useCallback((s: McpServer, previous?: string) => {
    setConn((c) => c && { ...c, servers: upsert(c.servers, s, byName, previous) });
  }, []);
  const putCli = useCallback((cli: Cli, previous?: string) => {
    setConn((c) => c && { ...c, clis: upsert(c.clis, cli, byId, previous) });
  }, []);

  useEffect(() => {
    if (!on) return;
    let stale = false;
    setLoading(true);
    setError("");
    commandRef
      .current<Connections>("list_connections", {})
      .then((r) => {
        if (stale) return;
        setConn({
          harnesses: r?.harnesses ?? [],
          servers: r?.servers ?? [],
          found: r?.found ?? [],
          clis: r?.clis ?? [],
        });
      })
      .catch((e: unknown) => !stale && setError(errorText(e)))
      .finally(() => !stale && setLoading(false));
    return () => {
      stale = true;
    };
  }, [on, seq, commandRef]);

  // The server never probes on a list, so after a restart everything reads
  // unchecked. Check those once, so the markers mean something; after that
  // only the Check buttons ask again. An answer is dropped if its entry was
  // removed, renamed or changed while the check ran: it describes the old one.
  useEffect(() => {
    if (!conn || checked.current) return;
    checked.current = true;
    for (const s of conn.servers) {
      if (s.url && s.status === "unchecked") {
        commandRef
          .current<{ server?: McpServer }>("check_mcp_server", { name: s.name })
          .then((r) => {
            const got = r?.server;
            if (got) setConn((c) => c && (c.servers.includes(s) ? { ...c, servers: upsert(c.servers, got, byName) } : c));
          })
          .catch(() => {});
      }
    }
    for (const cli of conn.clis) {
      if (cli.accounts.some((a) => a.status === "unchecked")) {
        commandRef
          .current<{ cli?: Cli }>("check_cli", { id: cli.id })
          .then((r) => {
            const got = r?.cli;
            if (got) setConn((c) => c && (c.clis.includes(cli) ? { ...c, clis: upsert(c.clis, got, byId) } : c));
          })
          .catch(() => {});
      }
    }
  }, [conn, commandRef]);

  const reload = useCallback(() => setSeq((n) => n + 1), []);

  const removeServer = useCallback((name: string) => {
    setConn(
      (c) =>
        c && {
          ...c,
          servers: c.servers.filter((s) => s.name !== name),
          found: c.found.map((f) => (f.name === name ? { ...f, added: false } : f)),
        },
    );
  }, []);

  const foundAdded = useCallback((from: FoundServer, s: McpServer) => {
    setConn(
      (c) =>
        c && {
          ...c,
          servers: upsert(c.servers, s, byName),
          found: c.found.map((f) => (f.name === from.name || f.name === s.name ? { ...f, added: true } : f)),
        },
    );
  }, []);

  const removeCli = useCallback((id: string) => {
    setConn((c) => c && { ...c, clis: c.clis.filter((x) => x.id !== id) });
  }, []);

  return { conn, error, loading, reload, putServer, removeServer, foundAdded, putCli, removeCli };
}

export interface LiveReport {
  report: ThreadMcpReport | null;
  loading: boolean;
  error: string;
  /** The server being reconnected. */
  busy: string | null;
  /** The last reconnect that failed, kept with its server's row. */
  failed: { name: string; error: string } | null;
  refresh: () => void;
  reconnect: (name: string) => Promise<void>;
}

/**
 * The open thread's running session, asked how each MCP server is doing.
 * Asked the first time the list is shown and on refresh, never polled: the
 * agent pushes nothing, and a reconnect answers with the new report.
 */
export function useLiveReport(command: PageCommand, threadId: string | undefined, shown: boolean): LiveReport {
  const [report, setReport] = useState<ThreadMcpReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [failed, setFailed] = useState<{ name: string; error: string } | null>(null);
  const [seq, setSeq] = useState(0);
  const commandRef = useLatest(command);
  const [on, setOn] = useState(shown);
  if (shown && !on) setOn(true);

  useEffect(() => {
    if (!threadId || !on) return;
    let stale = false;
    setLoading(true);
    setError("");
    setFailed(null);
    commandRef
      .current<ThreadMcpReport>("thread_mcp_status", { threadId })
      .then((r) => !stale && setReport({ live: !!r?.live, servers: r?.servers ?? [] }))
      .catch((e: unknown) => !stale && setError(errorText(e)))
      .finally(() => !stale && setLoading(false));
    return () => {
      stale = true;
    };
  }, [threadId, on, seq, commandRef]);

  const refresh = useCallback(() => setSeq((n) => n + 1), []);

  const reconnect = useCallback(
    async (name: string) => {
      if (!threadId) return;
      setBusy(name);
      setFailed(null);
      try {
        const r = await commandRef.current<ThreadMcpReport>("thread_mcp_reconnect", { threadId, name });
        setReport({ live: !!r?.live, servers: r?.servers ?? [] });
      } catch (e) {
        setFailed({ name, error: errorText(e) });
      } finally {
        setBusy(null);
      }
    },
    [threadId, commandRef],
  );

  return { report, loading, error, busy, failed, refresh, reconnect };
}
