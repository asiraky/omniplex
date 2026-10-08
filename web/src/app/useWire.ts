import { useCallback, useEffect, useRef, useState } from "react";

import { Client, wsURL, type ConnectionStatus } from "~/client";
import type {
  Access,
  HarnessMeta,
  Label,
  Project,
  QuotaStatus,
  ThreadMeta,
  ThreadState,
  UserConfig,
} from "~/protocol";
import { loadResume } from "~/resume";

import { LAST_THREAD } from "./threadKeys";

export type Wire = ReturnType<typeof useWire>;

/**
 * The socket, and everything the server pushes down it: the thread list, the
 * catalogues, and the state of the one thread attached. Everything else in the
 * app reads the server through this.
 */
export function useWire() {
  // The snapshot a previous page of this tab saved as it went to background
  // (resume.ts). A mobile browser discards a backgrounded tab and reloads it
  // on return; hydrating from the cache paints the thread as it was left,
  // right frame one, right scroll position, instead of "Attaching…", and the
  // socket then fetches only what the page missed. Cleared once consumed, and
  // if the thread turns out to be gone when the list arrives.
  const [resume, setResume] = useState(() => {
    try {
      return loadResume(localStorage.getItem(LAST_THREAD));
    } catch {
      return null;
    }
  });
  const dropResume = useCallback(() => setResume(null), []);
  // The copy the socket-setup effect reads: priming must use what the page
  // hydrated with, not what later clearing left behind.
  const resumeRef = useRef(resume);
  // The socket callbacks below outlive any single render, so they read the
  // attached thread from a ref rather than a captured closure. The ref is
  // written after commit, never during render (see useThreadSelection), so it
  // can only ever hold a value the UI actually rendered.
  const activeRef = useRef<string | null>(resume?.state.threadId ?? null);
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [threads, setThreads] = useState<ThreadMeta[]>([]);
  // True until the first thread list lands, which is when we know whether
  // the stored thread still exists.
  const [threadsLoaded, setThreadsLoaded] = useState(false);
  const [harnesses, setHarnesses] = useState<HarnessMeta[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  // The user's label definitions, server-owned: every mutation round-trips
  // and comes back as a broadcast, so paired devices all render the same set.
  const [labels, setLabels] = useState<Label[]>([]);
  // Every provider instance's cached usage limits, pushed by the server on
  // welcome and whenever a live push or refresh changes one.
  const [quotas, setQuotas] = useState<QuotaStatus[]>([]);
  const [access, setAccess] = useState<Access | null>(null);
  const [userConfig, setUserConfig] = useState<UserConfig | null>(null);
  const [state, setState] = useState<ThreadState | null>(resume?.state ?? null);
  // Read by long-lived callbacks (openPath) that must see the current state
  // without re-creating themselves on every event.
  const stateRef = useRef<ThreadState | null>(resume?.state ?? null);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);
  // Bumped when the attached thread's adapter says its slash commands and
  // skills changed, so the composer knows to ask for them again.
  const [composerRevision, setComposerRevision] = useState(0);
  const clientRef = useRef<Client | null>(null);

  useEffect(() => {
    const client = new Client(wsURL(), {
      onStatus: setStatus,
      onThreads: (list) => {
        setThreads(list);
        setThreadsLoaded(true);
      },
      onHarnesses: setHarnesses,
      onComposerItemsChanged: (id) => {
        if (id === activeRef.current) setComposerRevision((revision) => revision + 1);
      },
      onProjects: setProjects,
      onLabels: setLabels,
      onQuotas: setQuotas,
      // State only lands for the thread currently attached; the client
      // discards anything else.
      onState: (id, s) => {
        if (id === activeRef.current) setState(s);
      },
      onAccess: setAccess,
    });
    clientRef.current = client;
    // A resumed page attaches where it left off: the client carries the
    // cached state and cursor into its first attach, and the server answers
    // with just the gap.
    if (resumeRef.current) client.prime(resumeRef.current.state);
    client.connect();
    return () => client.close();
  }, []);

  // User-scope preferences are read once the socket is up, and again after a
  // reconnect only if we never got them; they change far less often than state.
  useEffect(() => {
    if (status !== "online" || userConfig) return;
    clientRef.current
      ?.command("get_user_config", {})
      .then((res) => setUserConfig(res.userConfig))
      .catch(() => {});
  }, [status, userConfig]);
  const saveUserConfig = useCallback(async (cfg: UserConfig) => {
    const res = await clientRef.current!.command("save_user_config", { config: cfg });
    setUserConfig(res.userConfig);
  }, []);

  // The resume cache is one boot's worth of help. Its scroll position moved
  // into the per-thread map as the page hydrated, and the transcript takes
  // over reporting from there, so once a thread is on screen the blob has
  // done its job.
  const hasThread = state != null;
  useEffect(() => {
    if (resume && hasThread) setResume(null);
  }, [resume, hasThread]);

  return {
    clientRef,
    resume,
    dropResume,
    activeRef,
    status,
    threads,
    setThreads,
    threadsLoaded,
    harnesses,
    setHarnesses,
    projects,
    setProjects,
    labels,
    setLabels,
    quotas,
    access,
    setAccess,
    userConfig,
    saveUserConfig,
    state,
    setState,
    stateRef,
    composerRevision,
  };
}
