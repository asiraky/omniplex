import { useCallback, useEffect, useRef, useState } from "react";

import { Client, wsURL, type ConnectionStatus } from "~/client";
import type {
  Access,
  Event,
  HarnessMeta,
  Project,
  QuotaStatus,
  ThreadMeta,
  ThreadState,
  TurnStatus,
  UserConfig,
  UserConfigReply,
} from "~/protocol";
import { threadIdFromPath } from "~/lib/threadLinks";
import { toast } from "~/lib/toast";
import { loadResume } from "~/resume";

import { reuseThreads } from "./reuseThreads";
import { LAST_THREAD } from "./threadKeys";
import { createThreadStore, useThread } from "./threadStore";

export type Wire = ReturnType<typeof useWire>;

/** Says out loud what an extension just said. Live events only: the replay on
    every attach would otherwise repeat every toast the thread ever raised.
    Warnings and errors also leave a notice in the transcript (apply.ts). */
function announce(ev: Event) {
  if (ev.type !== "extension.notified") return;
  const { level, message } = ev.payload ?? {};
  if (!message) return;
  const show = level === "error" ? toast.error : level === "warning" ? toast.warning : toast.info;
  show(message);
}

/**
 * The socket, and everything the server pushes down it: the thread list, the
 * catalogues, and the state of the one thread attached. Everything else in the
 * app reads the server through this.
 *
 * The attached thread's state is the one thing here not kept in React state:
 * it changes with every frame, and it lives in a store (threadStore.ts) that
 * each consumer selects its own slice of, so a frame rerenders only what it
 * changed rather than the app.
 */
export function useWire() {
  // The snapshot a previous page of this tab saved as it went to background
  // (resume.ts). A mobile browser discards a backgrounded tab and reloads it
  // on return; hydrating from the cache paints the thread as it was left,
  // right frame one, right scroll position, instead of "Attaching…", and the
  // socket then fetches only what the page missed. Cleared once consumed, and
  // if the thread turns out to be gone when the list arrives.
  //
  // Unless the page was opened at /thread/<id> (a tapped notification) for a
  // different thread: painting the last one first would flash the wrong
  // thread before the link took over.
  //
  // The boot thread is the one the restore will open if the list still has
  // it: the cached one, else the link, else the last thread. Without a cache
  // its snapshot is fetched while the socket is still connecting rather than
  // after the welcome, which on a slow connection is the round trips the
  // reader would otherwise spend looking at a spinner.
  const [link] = useState(() => threadIdFromPath(window.location.pathname));
  const [boot] = useState(() => {
    let last: string | null = null;
    let resume: ReturnType<typeof loadResume> = null;
    try {
      last = localStorage.getItem(LAST_THREAD);
      if (!link || link === last) resume = loadResume(last);
    } catch {
      // Storage denied: boot from the link alone.
    }
    return { resume, thread: resume?.state.threadId ?? link ?? last };
  });
  const [resume, setResume] = useState(boot.resume);
  const dropResume = useCallback(() => setResume(null), []);
  // The socket callbacks below outlive any single render, so they read the
  // attached thread from a ref rather than a captured closure. The ref is
  // written after commit, never during render (see useThreadSelection), so it
  // can only ever hold a value the UI actually rendered.
  const activeRef = useRef<string | null>(boot.thread);
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [threads, setThreads] = useState<ThreadMeta[]>([]);
  // True until the first thread list lands, which is when we know whether
  // the stored thread still exists.
  const [threadsLoaded, setThreadsLoaded] = useState(false);
  const [harnesses, setHarnesses] = useState<HarnessMeta[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  // Every provider instance's cached usage limits, pushed by the server on
  // welcome and whenever a live push or refresh changes one.
  const [quotas, setQuotas] = useState<QuotaStatus[]>([]);
  const [access, setAccess] = useState<Access | null>(null);
  const [userConfig, setUserConfig] = useState<UserConfig | null>(null);
  // Whether the server holds an ElevenLabs key, which is what turns read-aloud
  // on. Known only once the user config has arrived; off until then.
  const [hasElevenLabsKey, setHasElevenLabsKey] = useState(false);
  // Hydrated from the resume cache like the rest, so the first frame paints
  // the thread as it was left.
  const [threadStore] = useState(() => createThreadStore(resume?.state ?? null));
  // Bumped when the attached thread's adapter says its slash commands and
  // skills changed, so the composer knows to ask for them again.
  const [composerRevision, setComposerRevision] = useState(0);
  // Why the attached thread's running turn has gone quiet (retrying a model
  // request, compacting context). Transient: never in ThreadState, never
  // persisted. Tagged with its thread so a switch can never show the last
  // thread's status on the next one, even before the client's clear lands.
  const [heldStatus, setHeldStatus] = useState<{ threadId: string; status: TurnStatus } | null>(null);
  const clientRef = useRef<Client | null>(null);

  useEffect(() => {
    const client = new Client(wsURL(), {
      onStatus: setStatus,
      onThreads: (list) => {
        // Each broadcast is the whole list as fresh JSON; the threads that
        // did not change keep their objects, so their memoised rows skip it.
        setThreads((prev) => reuseThreads(prev, list));
        setThreadsLoaded(true);
      },
      onHarnesses: setHarnesses,
      onComposerItemsChanged: (id) => {
        if (id === activeRef.current) setComposerRevision((revision) => revision + 1);
      },
      onProjects: setProjects,
      onQuotas: setQuotas,
      // State only lands for the thread currently attached; the client
      // discards anything else.
      onState: (id, s) => {
        if (id === activeRef.current) threadStore.set(s);
      },
      onTurnStatus: (id, s) => setHeldStatus(s ? { threadId: id, status: s } : null),
      onAccess: setAccess,
      onLiveEvent: (id, ev) => {
        if (id === activeRef.current) announce(ev);
      },
    });
    clientRef.current = client;
    // A resumed page attaches where it left off: the client carries the
    // cached state and cursor into its first attach, and the server answers
    // with just the gap. Any other boot thread starts its snapshot fetch now,
    // alongside the handshake; the socket attaches from its cursor once both
    // are in. A thread that turns out to be gone is let go when the list
    // lands (useThreadSelection's restore).
    if (boot.resume) client.prime(boot.resume.state);
    else if (boot.thread) client.attach(boot.thread);
    client.connect();
    return () => client.close();
  }, [boot, threadStore]);

  // User-scope preferences are read once the socket is up, and again after a
  // reconnect only if we never got them; they change far less often than state.
  useEffect(() => {
    if (status !== "online" || userConfig) return;
    clientRef.current
      ?.command("get_user_config", {})
      .then((res: UserConfigReply) => {
        setUserConfig(res.userConfig);
        setHasElevenLabsKey(res.hasElevenLabsKey);
      })
      .catch(() => {});
  }, [status, userConfig]);
  const saveUserConfig = useCallback(async (cfg: UserConfig) => {
    const res: UserConfigReply = await clientRef.current!.command("save_user_config", {
      config: cfg,
    });
    setUserConfig(res.userConfig);
    setHasElevenLabsKey(res.hasElevenLabsKey);
  }, []);
  // Empty removes the stored key. A key ElevenLabs refuses rejects with its
  // reason, and the stored one (if any) stays.
  const setElevenLabsKey = useCallback(async (key: string) => {
    const res: { hasElevenLabsKey: boolean } = await clientRef.current!.command(
      "set_elevenlabs_key",
      { key },
    );
    setHasElevenLabsKey(res.hasElevenLabsKey);
  }, []);

  // The resume cache is one boot's worth of help. Its scroll position moved
  // into the per-thread map as the page hydrated, and the transcript takes
  // over reporting from there, so once a thread is on screen the blob has
  // done its job.
  const attached = useThread(threadStore, isAttached);
  useEffect(() => {
    if (resume && attached) setResume(null);
  }, [resume, attached]);

  const attachedId = useThread(threadStore, threadIdOf);
  const turnStatus = heldStatus && heldStatus.threadId === attachedId ? heldStatus.status : null;

  return {
    clientRef,
    /** The thread the page's /thread/<id> URL named at load, if any. */
    link,
    /** The thread attached at load, before the list could say it exists, if any. */
    bootThread: boot.thread,
    resume,
    dropResume,
    activeRef,
    status,
    threads,
    threadsLoaded,
    harnesses,
    setHarnesses,
    projects,
    setProjects,
    quotas,
    access,
    setAccess,
    userConfig,
    saveUserConfig,
    hasElevenLabsKey,
    setElevenLabsKey,
    /** The attached thread's state: read it with useThread, or get() it at call time. */
    threadStore,
    /** Whether a thread's state is on hand, which is all the app renders from it. */
    attached,
    composerRevision,
    turnStatus,
  };
}

function isAttached(s: unknown) {
  return s != null;
}

const threadIdOf = (s: ThreadState | null) => s?.threadId;
