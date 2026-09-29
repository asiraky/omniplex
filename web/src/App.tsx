import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Client, uuid, wsURL, type ConnectionStatus } from "./client";
import { useIsDesktop } from "./useMediaQuery";
import { useDocumentTitle } from "./useDocumentTitle";
import { useThreadPR } from "./useThreadPR";
import type { Access, AuthFlowEvent, ComposerItem, FileContent, FileDiff, FileTree, HarnessMeta, Label, Folder, GitHubRepo, Project, ProjectDefaults, QuotaStatus, ThreadChanges, ThreadMeta, ThreadState, PullRequest, UsageReport, UserConfig, Workspace } from "./protocol";
import { AccessPanel } from "./components/Access";
import type { PanelRequest } from "./components/panel/Panel";
import { liveJobCount } from "./lib/jobs";
import { OpenPathContext } from "./lib/openPath";
import { Composer, type ComposerHandle } from "./components/Composer";
import { ScheduleDialog, ScheduledPrompts, type ScheduleInput } from "./components/ScheduledPrompts";
import type { ScheduledPrompt, Turn } from "./protocol";
import type { NewProjectRequest } from "./components/NewProject";
import type { AddFolderRequest } from "./components/ProjectSettings";
import type { SettingsSection } from "./components/SettingsScreen";
import { JobsStrip } from "./components/JobsStrip";
import { ThreadDraft } from "./components/ThreadDraft";
import type { NewThreadInput } from "./components/ThreadDraft";
import { PermissionPrompt } from "./components/PermissionPrompt";
import { ElicitationPrompt } from "./components/ElicitationPrompt";
import { DeleteThreadDialog, useDeleteThread } from "./components/DeleteThreadDialog";
import { LabelManager } from "./components/LabelManager";
import { LabelDot, LabelMenu, LabelMenuItems } from "./components/LabelMenu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "./components/ui/dropdown-menu";
import { Sidebar } from "./components/Sidebar";
import { Transcript } from "./components/Transcript";
import { IconButton } from "./components/IconButton";
import { Button } from "./components/ui/button";
import { Spinner } from "./components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./components/ui/select";
import { sendPayload, stageFile, uploadStaged, type Attachment } from "./lib/attachments";
import { loadRecentSkills, recordRecentSkill, resolveRecentSkills } from "./lib/recentSkills";
import { loadResume } from "./resume";
import { cn } from "./lib/utils";
import { transcriptMarkdown } from "./lib/transcript";
import { useCopy } from "./lib/clipboard";
import {
  CheckIcon,
  CoffeeIcon,
  CopyIcon,
  EllipsisIcon,
  MessagesSquareIcon,
  PanelLeftIcon,
  PanelRightIcon,
  PlusIcon,
  TagIcon,
} from "lucide-react";
import { toast } from "sonner";

const LAST_THREAD = "omniplex.lastThread";
// The drafts key for a thread not yet started.
const NEW_THREAD = "new-thread";

const Panel = lazy(() => import("./components/panel/Panel").then((m) => ({ default: m.Panel })));
const NewProject = lazy(() => import("./components/NewProject").then((m) => ({ default: m.NewProject })));
const SettingsScreen = lazy(() => import("./components/SettingsScreen").then((m) => ({ default: m.SettingsScreen })));
// The sign-in dialog carries xterm; it stays out of the first load like the Panel does.
const LoginDialog = lazy(() => import("./components/LoginDialog").then((m) => ({ default: m.LoginDialog })));
const InstanceAuthDialog = lazy(() => import("./components/AuthFlowDialog"));
const ThemePreview = lazy(() => import("./components/ThemePreview").then((m) => ({ default: m.ThemePreview })));
const UsagePage = lazy(() => import("./components/Usage").then((m) => ({ default: m.UsagePage })));

// The permission-mode switcher is parked, not removed: changing modes mid-chat
// is not something we want to offer right now, and hiding it is cheaper to
// reverse than deleting it. Flip this to bring it back.
const SHOW_MODE_SWITCHER = false;

export function App() {
  const [scheduleEditor, setScheduleEditor] = useState<{id: string; threadId: string; text: string; imageIds: string[]; schedule?: ScheduledPrompt} | null>(null);
  const { copied: transcriptCopied, copy: copyTranscript } = useCopy();
  // The snapshot a previous page of this tab saved as it went to background
  // (resume.ts). A mobile browser discards a backgrounded tab and reloads it
  // on return; hydrating from the cache paints the thread as it was left —
  // right frame one, right scroll position — instead of "Attaching…", and the
  // socket then fetches only what the page missed. Cleared once consumed, and
  // if the thread turns out to be gone when the list arrives.
  const [resume, setResume] = useState(() => {
    try {
      return loadResume(localStorage.getItem(LAST_THREAD));
    } catch {
      return null;
    }
  });
  // The copy the socket-setup effect reads: priming must use what the page
  // hydrated with, not what later clearing left behind.
  const resumeRef = useRef(resume);
  // The socket callbacks below outlive any single render, so they read the
  // attached thread from a ref rather than a captured closure. The ref is
  // written after commit, never during render, so it can only ever hold a
  // value the UI actually rendered.
  const activeRef = useRef<string | null>(resume?.state.threadId ?? null);
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [threads, setThreads] = useState<ThreadMeta[]>([]);
  const [harnesses, setHarnesses] = useState<HarnessMeta[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  // The user's label definitions, server-owned: every mutation round-trips
  // and comes back as a broadcast, so paired devices all render the same set.
  const [labels, setLabels] = useState<Label[]>([]);
  const [manageLabels, setManageLabels] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(resume?.state.threadId ?? null);
  const [state, setState] = useState<ThreadState | null>(resume?.state ?? null);
  // Read by long-lived callbacks (openPath) that must see the current state
  // without re-creating themselves on every event.
  const stateRef = useRef<ThreadState | null>(resume?.state ?? null);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);
  // Composer drafts, kept per thread up here rather than inside the Composer.
  // Switching threads nulls `state`, which unmounts the whole content subtree
  // (Composer included) and remounts it for the next thread — so a draft owned
  // by the Composer would be destroyed on every switch. Holding it in the
  // parent, keyed by thread id, lets a half-typed message survive the swap and
  // still be there when you come back. Thread scope only: no persistence, and
  // the map is pruned as threads go away (see below).
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // Images staged for the next message, per thread and for the same reason as
  // the drafts: switching away and back must not lose what you attached. The
  // upload starts as soon as a picture is picked, so by send time this is a
  // list of ids the server already holds.
  const [attachments, setAttachments] = useState<Record<string, Attachment[]>>({});
  const [composerRevision, setComposerRevision] = useState(0);
  // Where each thread's transcript was scrolled, kept up here for the same
  // reason as the drafts: switching threads unmounts the Transcript, so a
  // position it owned would be lost every time — you would come back to a
  // thread you were reading half-way up and find yourself at the bottom.
  // A ref rather than state: the transcript reports every scroll, and nothing
  // on the page renders from this, so re-rendering the app on each one would
  // be pure cost. Seeded from the resume cache so the boot restore and the
  // switch restore are one path. Thread scope only, pruned with the drafts.
  const scrollPositions = useRef<Record<string, { top: number; atBottom: boolean }>>(
    resume ? { [resume.state.threadId]: { top: resume.scrollTop, atBottom: resume.atBottom } } : {},
  );
  // Threads the list has taken away. A deleted thread's transcript reports
  // one last position as it unmounts, and that unmount happens after the prune
  // below has already dropped it — so the id is refused outright rather than
  // being written straight back in.
  const goneThreads = useRef<Set<string>>(new Set());
  const recordScroll = useCallback((id: string, top: number, atBottom: boolean) => {
    if (goneThreads.current.has(id)) return;
    scrollPositions.current[id] = { top, atBottom };
  }, []);
  const setDraft = useCallback(
    (id: string, text: string) =>
      setDrafts((d) => (d[id] === text ? d : { ...d, [id]: text })),
    [],
  );
  const patchAttachment = useCallback((threadId: string, key: string, patch: Partial<Attachment>) => {
    setAttachments((all) => {
      const list = all[threadId];
      if (!list?.some((a) => a.key === key)) return all;
      return { ...all, [threadId]: list.map((a) => (a.key === key ? { ...a, ...patch } : a)) };
    });
  }, []);

  // Picked, dropped, or pasted images. Each is uploaded on its own the moment
  // it arrives: the composer stays usable, and a slow picture on a slow
  // connection never blocks typing the question that goes with it.
  // The in-flight upload behind each staged image, so removing one can stop it.
  const uploadsInFlight = useRef<Map<string, AbortController>>(new Map());

  // One file up to one thread. Resolves to what made it sendable; rejects when
  // it failed or was taken back, having already marked it in the composer.
  const uploadInto = useCallback(
    (threadId: string, key: string, file: File) => {
      const abort = new AbortController();
      uploadsInFlight.current.set(key, abort);
      return uploadStaged(threadId, file, {
        signal: abort.signal,
        onProgress: (progress) => patchAttachment(threadId, key, { progress }),
      })
        .then((patch) => {
          patchAttachment(threadId, key, patch);
          return patch;
        })
        .catch((e: Error) => {
          // An abort means the file was taken back; there is nothing left
          // to report it to.
          if (e.name !== "AbortError") patchAttachment(threadId, key, { status: "error", error: e.message });
          throw e;
        })
        .finally(() => uploadsInFlight.current.delete(key));
    },
    [patchAttachment],
  );

  const attachImages = useCallback(
    (files: File[]) => {
      const threadId = activeId;
      if (!threadId) return;
      for (const file of files) {
        // Not `crypto.randomUUID`: that exists only in a secure context, and
        // the origins a phone reaches this server on are not one.
        const key = uuid();
        const staged = stageFile(file, key);
        setAttachments((all) => ({ ...all, [threadId]: [...(all[threadId] ?? []), staged] }));
        // A picture goes up as an image, shrunk first; anything else goes up
        // as an artefact the agent reads from disk.
        uploadInto(threadId, key, file).catch(() => {});
      }
    },
    [activeId, uploadInto],
  );

  // Files picked for a thread that does not exist yet. Uploads belong to a
  // thread, so these wait here as they were picked and go up once it does.
  const draftFiles = useRef<Map<string, File>>(new Map());
  const attachToDraft = useCallback((files: File[]) => {
    for (const file of files) {
      const key = uuid();
      draftFiles.current.set(key, file);
      const staged: Attachment = { ...stageFile(file, key), status: "staged", progress: undefined };
      setAttachments((all) => ({ ...all, [NEW_THREAD]: [...(all[NEW_THREAD] ?? []), staged] }));
    }
  }, []);

  const removeAttachment = useCallback((threadId: string, key: string) => {
    uploadsInFlight.current.get(key)?.abort();
    draftFiles.current.delete(key);
    setAttachments((all) => {
      const list = all[threadId] ?? [];
      const going = list.find((a) => a.key === key);
      if (going?.previewUrl) URL.revokeObjectURL(going.previewUrl);
      return { ...all, [threadId]: list.filter((a) => a.key !== key) };
    });
  }, []);

  const isDesktop = useIsDesktop();
  // Whether the last-thread key was set at boot. Read once, before anything
  // can write it, because it decides what the very first frame shows. Storage
  // can be denied outright (Safari with cookies blocked), and a throw here
  // would take the whole mount with it.
  const [hadLastThread] = useState(() => {
    try {
      return localStorage.getItem(LAST_THREAD) !== null;
    } catch {
      return false;
    }
  });
  // True until the first thread list lands, which is when we know whether
  // the stored thread still exists. Until then a phone must not flash the
  // sidebar open and then shut it again a moment later.
  const [threadsLoaded, setThreadsLoaded] = useState(false);
  const restoreAttempted = useRef(false);
  // Open is the desktop default. On a phone the sidebar *is* the landing
  // screen: with nothing selected there is nothing behind it to look at, so
  // it starts open unless we are about to restore straight into a thread.
  const [sidebarOpen, setSidebarOpen] = useState(() => isDesktop || !hadLastThread);
  // Crossing the breakpoint resets it — but only on an actual crossing. On
  // mount this must leave the initial choice above alone.
  const wasDesktop = useRef(isDesktop);
  useEffect(() => {
    if (wasDesktop.current === isDesktop) return;
    wasDesktop.current = isDesktop;
    setSidebarOpen(isDesktop || activeRef.current === null);
  }, [isDesktop]);
  // A thread being written but not yet sent, and the project it opened on.
  const [creating, setCreating] = useState<{ projectId?: string } | null>(null);
  // Open, and at which section; the sidebar gear opens it at the top.
  const [settings, setSettings] = useState<{ at?: SettingsSection } | null>(null);
  const [newProject, setNewProject] = useState(false);
  const [userConfig, setUserConfig] = useState<UserConfig | null>(null);
  const [access, setAccess] = useState<Access | null>(null);
  const [showAccess, setShowAccess] = useState(false);
  const [showChanges, setShowChanges] = useState(false);
  const [panelLoaded, setPanelLoaded] = useState(false);
  useEffect(() => {
    if (showChanges) setPanelLoaded(true);
  }, [showChanges]);
  // One click takes the diff panel to the full content width; another brings
  // it back. There is no in-between state on purpose.
  const [changesExpanded, setChangesExpanded] = useState(false);
  // The theme sample page: a static mock of the dashboard behind a palette
  // switcher, reachable at #themes so it needs no router.
  const [themePreview, setThemePreview] = useState(() => window.location.hash === "#themes");
  useEffect(() => {
    const onHash = () => setThemePreview(window.location.hash === "#themes");
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  // The account-level Usage page: cost history, token history, and the
  // providers' remaining allowance. A full-page destination, not a thread
  // view — it never needs one attached.
  const [showUsage, setShowUsage] = useState(false);
  // Every provider instance's cached usage limits, pushed by the server on
  // welcome and whenever a live push or refresh changes one.
  const [quotas, setQuotas] = useState<QuotaStatus[]>([]);
  // What the panel should put on screen, and a counter that changes on every
  // request. Without the counter, asking for the same file twice would look
  // identical to the panel and it would not bring it back into view.
  const [panelRequest, setPanelRequest] = useState<PanelRequest | null>(null);

  // Opening the diff from a turn's card: show the panel, and put it on the file
  // that was clicked.
  const openDiff = useCallback((path?: string) => {
    setShowChanges(true);
    setPanelRequest((current) => ({ kind: "diff", path, nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  // Opening a path clicked in prose. The panel routes it: the diff surface
  // when the thread changed it, the file surface otherwise. An absolute path
  // under the checkout is relativised first; the server only serves the
  // workspace.
  // Opening the jobs surface from the strip or a spawn card in the transcript.
  const panelCommand = useCallback(
    (cmd: string, args: unknown) => clientRef.current!.command(cmd, args),
    [],
  );
  const openJobs = useCallback(() => {
    setShowChanges(true);
    setPanelRequest((current) => ({ kind: "jobs", nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  // Opening an artefact from its card in the transcript.
  const openArtefact = useCallback((artefactId: string) => {
    setShowChanges(true);
    setPanelRequest((current) => ({ kind: "artefact", artefactId, nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  const openPath = useCallback((path: string, line?: number) => {
    const cwd = stateRef.current?.cwd ?? "";
    let rel = path;
    if (cwd && (rel === cwd || rel.startsWith(cwd + "/"))) rel = rel.slice(cwd.length).replace(/^\//, "");
    if (rel === "") return;
    setShowChanges(true);
    setPanelRequest((current) => ({ kind: "path", path: rel, line, nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  const clientRef = useRef<Client | null>(null);
  const forcePromptedRef = useRef<string | null>(null);
  // The floating overlay (composer plus any permission/elicitation prompt
  // stacked above it) and the column it floats over. We measure the first and
  // publish its height on the second, so the transcript can reserve exactly
  // that much room beneath its content.
  const chatLayoutRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    activeRef.current = activeId;
  }, [activeId]);

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
    clientRef.current?.command("get_user_config", {}).then(res => setUserConfig(res.userConfig)).catch(() => {});
  }, [status, userConfig]);

  // Restore the last thread once the list arrives. This runs once: after it,
  // "no thread selected" is a state the user chose, not one we have yet to
  // resolve, and re-opening the sidebar under them would be wrong.
  useEffect(() => {
    if (!threadsLoaded || restoreAttempted.current) return;
    restoreAttempted.current = true;
    if (activeId) {
      // Hydrated from the resume cache before the list could say whether the
      // thread still exists. It usually does; when it doesn't — deleted or
      // closed from elsewhere while the page was dead — let go the same way
      // a live delete would. The seenActive effect below can't: it only acts
      // on threads it saw in a list first.
      if (threads.some((s) => s.id === activeId && s.phase !== "closed")) return;
      // Including the position the cache seeded: the prune below only drops
      // threads it saw in a list, and this one never made it into one.
      delete scrollPositions.current[activeId];
      goneThreads.current.add(activeId);
      setActiveId(null);
      setState(null);
      setResume(null);
      clientRef.current?.detach();
      if (!isDesktop) setSidebarOpen(true);
      return;
    }
    const last = localStorage.getItem(LAST_THREAD);
    const pick = threads.find((s) => s.id === last && s.phase !== "closed") ?? null;
    if (pick) select(pick.id);
    // Nothing to restore into, so the phone lands on the sidebar after all.
    else if (!isDesktop) setSidebarOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadsLoaded, threads]);
  // Until the first list lands we do not know whether there is anything to
  // show, so the content column holds the space rather than announcing "all
  // caught up" to someone with six threads on a slow connection.
  const restoring = !threadsLoaded;

  const select = useCallback(
    (id: string) => {
      setCreating(null);
      setActiveId(id);
      activeRef.current = id;
      // The panel belongs to a checkout, so it must not survive a move to a
      // different one.
      setShowChanges(false);
      setChangesExpanded(false);
      // A file asked for in one thread means nothing in the next, and another
      // thread holding the same path would otherwise open it unasked.
      setPanelRequest(null);
      setState(null);
      localStorage.setItem(LAST_THREAD, id);
      clientRef.current?.attach(id);
      if (!isDesktop) setSidebarOpen(false);
    },
    [isDesktop],
  );

  // The draft takes the thread's place: nothing is attached while it is up,
  // and picking a thread from the list leaves it.
  const startNew = useCallback(() => {
    const projectId = threads.find((t) => t.id === activeRef.current)?.projectId;
    setCreating({ projectId });
    if (activeRef.current) {
      activeRef.current = null;
      setActiveId(null);
      setState(null);
      setResume(null);
      clientRef.current?.detach();
    }
    if (!isDesktop) setSidebarOpen(false);
  }, [isDesktop, threads]);

  // Starting a thread. A plain message goes with the thread. Files cannot: they
  // upload to a thread, so the thread starts empty, the message waits in its
  // composer while they go up, and then it sends. Scheduling starts it empty
  // too, and opens the schedule on it.
  const create = useCallback(
    async (input: NewThreadInput, schedule = false) => {
      const pending = (attachments[NEW_THREAD] ?? []).flatMap((a) => {
        const file = draftFiles.current.get(a.key);
        return file ? [{ a, file }] : [];
      });
      const later = schedule || pending.length > 0;
      const res = await clientRef.current!.command("create_thread", later ? { ...input, text: "" } : input);
      const threadId: string = res.threadId;
      setDraft(NEW_THREAD, "");
      for (const { a } of pending) draftFiles.current.delete(a.key);
      setAttachments((all) => {
        const next: Record<string, Attachment[]> = { ...all, [NEW_THREAD]: [] };
        if (pending.length) {
          next[threadId] = pending.map(({ a }) => ({ ...a, status: "uploading" as const, ...(a.kind === "file" ? { progress: 0 } : {}) }));
        }
        return next;
      });
      select(threadId);
      // The thread exists but the message did not go: it waits in the new
      // thread's composer rather than being lost.
      if (res.promptError) {
        setDraft(threadId, input.text);
        toast.error("The thread started, but the message did not send", {
          description: res.promptError,
        });
      }
      if (!later) return;

      setDraft(threadId, input.text);
      const results = await Promise.allSettled(pending.map(({ a, file }) => uploadInto(threadId, a.key, file)));
      const failed = results.filter((r): r is PromiseRejectedResult => r.status === "rejected");
      if (failed.length) {
        // Taken back mid-upload is a choice, not a failure; either way the
        // message stays in the composer for another go.
        if (failed.some((r) => r.reason?.name !== "AbortError")) {
          toast.error("The thread started, but a file did not upload", {
            description: "Your message is waiting in the thread's composer.",
          });
        }
        return;
      }
      const payload = sendPayload(
        pending.map(({ a }, i) => ({ ...a, ...(results[i] as PromiseFulfilledResult<Partial<Attachment>>).value })),
      );
      if (schedule) {
        setScheduleEditor({ id: uuid(), threadId, text: input.text, imageIds: payload.imageIds });
        return;
      }
      try {
        await clientRef.current!.command("prompt", {
          threadId,
          text: input.text,
          ...(payload.imageIds.length ? { imageIds: payload.imageIds } : {}),
          ...(payload.files.length ? { files: payload.files } : {}),
        });
      } catch (e) {
        toast.error("The thread started, but the message did not send", {
          description: e instanceof Error ? e.message : String(e),
        });
        return;
      }
      setDrafts((all) => (all[threadId] === input.text ? { ...all, [threadId]: "" } : all));
      setAttachments((all) => {
        for (const a of all[threadId] ?? []) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
        return { ...all, [threadId]: [] };
      });
    },
    [attachments, select, setDraft, uploadInto],
  );

  const listWorkspaces = useCallback(async (projectId: string, folderId: string) => {
    const res = await clientRef.current!.command("list_workspaces", { projectId, folderId });
    return (res.workspaces ?? []) as Workspace[];
  }, []);
  // Its own request: `gh` can take seconds, and nothing that shapes a choice
  // should be waiting behind it.
  const listIssues = useCallback(async (projectId: string, folderId: string) => {
    const res = await clientRef.current!.command("list_issues", { projectId, folderId });
    return { issues: res.issues ?? [], issuesError: res.issuesError ?? "" };
  }, []);
  const saveUserConfig = useCallback(async (cfg: UserConfig) => { const res=await clientRef.current!.command("save_user_config",{config:cfg}); setUserConfig(res.userConfig); },[]);

  // The transcript asking for the page above its window. Fire-and-forget: the
  // client dedups concurrent asks and publishes the merged state through the
  // same onState path every other update takes.
  const loadOlderItems = useCallback(() => {
    void clientRef.current?.loadOlder();
  }, []);

  // Copying wants the whole timeline, and a windowed state only holds the
  // tail — so pull the rest in first. What arrives stays loaded, which is
  // exactly what a reader who just copied everything would expect. A failed
  // fetch aborts the copy loudly: a truncated transcript that says "Copied"
  // is a lie pasted somewhere the truncation won't be noticed.
  const copyFullTranscript = useCallback(async () => {
    let s = stateRef.current;
    if (!s) return;
    if ((s.itemsBefore ?? 0) > 0) {
      const full = await clientRef.current?.loadAll();
      if (!full) {
        toast.error("Could not load the full transcript to copy");
        return;
      }
      s = full;
    }
    await copyTranscript(transcriptMarkdown(s.items, s.turns));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [copyTranscript]);

  // Read-on-open, reported to the server so paired devices agree. The report
  // carries the seq this page has actually rendered, not the server's head —
  // events landing mid-report stay unread. Gated on the phase: while a turn
  // streams, every event bumps seq, and re-reporting each one would chatter
  // on exactly the connections we care about. The turn finishing flips the
  // phase and sends one report for the whole turn.
  const viewedReported = useRef<Record<string, number>>({});
  // One chain of read-state commands per thread. The server runs each
  // connection's commands in independent goroutines, so two frames sent
  // back-to-back can execute in either order — and "mark unread" losing to an
  // in-flight read-on-open report would silently undo the user's click.
  // Sending each command only after the previous one's ack pins the order.
  const readStateQueue = useRef<Record<string, Promise<unknown>>>({});
  const sendReadState = useCallback((threadId: string, command: string, args: object) => {
    const next = (readStateQueue.current[threadId] ?? Promise.resolve()).then(
      () => clientRef.current?.command(command, args),
    );
    // Swallowed here so the chain survives a failure; callers hang their own
    // error handling off the returned promise.
    readStateQueue.current[threadId] = next.catch(() => {});
    return next;
  }, []);
  useEffect(() => {
    if (!state || state.threadId !== activeId) return;
    if (state.phase === "turn" || state.phase === "provisioning" || state.phase === "cleaning") return;
    if (state.seq <= (viewedReported.current[state.threadId] ?? 0)) return;
    viewedReported.current[state.threadId] = state.seq;
    sendReadState(state.threadId, "mark_thread_viewed", {
      threadId: state.threadId,
      seq: state.seq,
    }).catch(() => {
      // Nothing to tell the user: the dot clears next time this succeeds.
    });
  }, [activeId, state, sendReadState]);

  // The explicit flag back the other way, from the row's context menu.
  // Fire-and-forget like the label mutations: the threads broadcast is the
  // authoritative answer.
  const setThreadUnread = useCallback((threadId: string, unread: boolean) => {
    if (unread) {
      // Forget what this page reported, or the effect above would treat the
      // current head as already-sent and never re-mark it read.
      delete viewedReported.current[threadId];
      sendReadState(threadId, "mark_thread_unread", { threadId }).catch((e) => {
        toast.error("Could not mark that thread unread", { description: e.message });
      });
      return;
    }
    const head = threads.find((s) => s.id === threadId)?.headSeq ?? 0;
    sendReadState(threadId, "mark_thread_viewed", { threadId, seq: head }).catch((e) => {
      toast.error("Could not mark that thread read", { description: e.message });
    });
  }, [threads, sendReadState]);

  // Label mutations fire and forget: the authoritative answer arrives as a
  // labels (or threads) broadcast, the same way it does for a paired device,
  // so there is no local state to reconcile — only failures to report.
  const setThreadLabel = useCallback((threadId: string, labelId: string) => {
    clientRef.current?.command("set_thread_label", { threadId, labelId }).catch((e) => {
      toast.error("Could not label that thread", { description: e.message });
    });
  }, []);
  const createLabel = useCallback((name: string, color: string) => {
    clientRef.current?.command("create_label", { name, color }).catch((e) => {
      toast.error("Could not create that label", { description: e.message });
    });
  }, []);
  const saveLabel = useCallback((label: Label) => {
    // Apply locally before the round-trip: a second edit made before the
    // broadcast lands (recolour, then flip the collapse switch) must derive
    // from this save, not from the stale snapshot, or the later save silently
    // reverts the earlier field. The broadcast then settles the true state.
    setLabels((ls) =>
      ls
        .map((l) => (l.id === label.id ? label : l))
        .sort((a, b) => a.position - b.position || a.createdAt - b.createdAt || (a.id < b.id ? -1 : 1)),
    );
    clientRef.current
      ?.command("save_label", {
        labelId: label.id,
        name: label.name,
        color: label.color,
        position: label.position,
      })
      .catch((e) => {
        toast.error("Could not save that label", { description: e.message });
      });
  }, []);
  const deleteLabel = useCallback((id: string) => {
    clientRef.current?.command("delete_label", { labelId: id }).catch((e) => {
      toast.error("Could not delete that label", { description: e.message });
    });
  }, []);
  const openLabelManager = useCallback(() => setManageLabels(true), []);

  const putProject = useCallback((project: Project) => {
    setProjects((p) => [project, ...p.filter((x) => x.id !== project.id)]);
    return project;
  }, []);
  const createProject = useCallback(
    async (req: NewProjectRequest) =>
      putProject((await clientRef.current!.command("create_project", req)).project as Project),
    [putProject],
  );
  const addFolder = useCallback(
    async (projectId: string, req: AddFolderRequest) =>
      putProject((await clientRef.current!.command("add_folder", { projectId, ...req })).project as Project),
    [putProject],
  );
  const removeFolder = useCallback(
    async (projectId: string, folderId: string) =>
      putProject((await clientRef.current!.command("remove_folder", { projectId, folderId })).project as Project),
    [putProject],
  );
  const listRepos = useCallback(
    async () => (await clientRef.current!.command("list_github_repos", {})).repos as GitHubRepo[],
    [],
  );
  // A project's own settings and each changed folder's are separate saves; the
  // last answer carries every one of them.
  const saveProject = useCallback(async (projectId: string, name: string, defaults: ProjectDefaults, folders: Folder[]) => {
    let res = await clientRef.current!.command("save_project", { projectId, name, defaults });
    for (const folder of folders) res = await clientRef.current!.command("save_folder", { projectId, folder });
    setProjects((p) => p.map((x) => (x.id === projectId ? res.project : x)));
  }, []);
  // Forgetting a project touches nothing on disk, so the only thing to undo
  // locally is the list. The server broadcasts the new one to every other
  // device anyway; dropping it here just means this one does not wait for it.
  const deleteProject = useCallback(async (projectId: string) => {
    await clientRef.current!.command("delete_project", { projectId });
    setProjects((p) => p.filter((x) => x.id !== projectId));
  }, []);

  // Git is the source of truth for what a thread changed: it catches the
  // formatter and the codemod as well as the edits we parsed out of tool calls.
  const loadChanges = useCallback(async (comparison: import("./protocol").DiffComparison) => {
    const res = await clientRef.current!.command("thread_changes", { threadId: activeId, comparison });
    return res.changes as ThreadChanges;
  }, [activeId]);

  const loadFileDiff = useCallback(
    async (path: string, changes: ThreadChanges) => {
      const res = await clientRef.current!.command("thread_file_diff", {
        threadId: activeId,
        path,
        comparison: changes.mode,
        base: changes.base,
        head: changes.head,
      });
      return res.diff as FileDiff;
    },
    [activeId],
  );

  // The real filesystem, for the files and file surfaces: git is the diff
  // surface, and a file the thread never touched is exactly what it can't show.
  const loadFileTree = useCallback(
    async (includeIgnored: boolean) => {
      const res = await clientRef.current!.command("thread_file_tree", { threadId: activeId, includeIgnored });
      return res.tree as FileTree;
    },
    [activeId],
  );

  const loadFile = useCallback(
    async (path: string) => {
      const res = await clientRef.current!.command("thread_read_file", { threadId: activeId, path });
      return res.file as FileContent;
    },
    [activeId],
  );

  const send = useCallback(
    (text: string) => {
      if (!activeId) return;
      const staged = attachments[activeId] ?? [];
      const { imageIds, files } = sendPayload(staged);
      // Left out entirely when there are none: the overwhelming majority of
      // prompts carry nothing, and the frame is persisted for retry.
      const args = {
        threadId: activeId,
        text,
        ...(imageIds.length ? { imageIds } : {}),
        ...(files.length ? { files } : {}),
      };
      clientRef.current?.command("prompt", args).catch((e) => {
        toast.error("Could not send that prompt", { description: e.message });
      });
      // Cleared optimistically, like the draft: the message is on its way, and
      // the transcript is about to show the same pictures back from the server.
      for (const a of staged) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
      setAttachments((all) => (all[activeId]?.length ? { ...all, [activeId]: [] } : all));
    },
    [activeId, attachments],
  );

  async function saveSchedule(input: ScheduleInput) {
    const editor = scheduleEditor;
    if (!editor || !clientRef.current) throw new Error("Reconnect before scheduling");
    await clientRef.current.command("schedule_prompt", { threadId: editor.threadId, id: editor.schedule?.id ?? editor.id, revision: editor.schedule?.revision ?? 0, ...input, imageIds: editor.imageIds });
    if (!editor.schedule) {
      // Clear only the draft and images captured when this sheet opened.
      setDrafts(all => all[editor.threadId] === editor.text ? {...all, [editor.threadId]: ""} : all);
      setAttachments(all => {
        const staged = all[editor.threadId] ?? [];
        for (const a of staged) if (a.id && a.previewUrl && editor.imageIds.includes(a.id)) URL.revokeObjectURL(a.previewUrl);
        return {...all, [editor.threadId]: staged.filter(a => !a.id || !editor.imageIds.includes(a.id))};
      });
    }
  }

  const loadComposerItems = useCallback(async (): Promise<ComposerItem[]> => {
    if (!activeId) return [];
    const result = await clientRef.current!.command("list_composer_items", { threadId: activeId });
    return result.items ?? [];
  }, [activeId, composerRevision]);

  const runComposerAction = useCallback(
    async (action: string, args: string, invocation: string) => {
      if (!activeId) return;
      try {
        await clientRef.current!.command("run_composer_action", {
          threadId: activeId,
          action,
          args,
          invocation,
        });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        toast.error("Could not run that command", { description: message });
        throw error;
      }
    },
    [activeId],
  );

  const runClientComposerAction = useCallback(
    (action: string) => {
      if (action === "diff") {
        setShowChanges(true);
        setPanelRequest((current) => ({
          kind: "diff",
          nonce: (current?.nonce ?? 0) + 1,
        }));
        return;
      }
      if (action === "status" && state) {
        const used = state.usage?.contextUsed;
        const parts = [
          state.model || "Default model",
          state.mode || "Default approvals",
          used !== undefined ? `${used.toLocaleString()} context tokens` : "Token usage unavailable",
        ];
        toast.info("Thread status", { description: parts.join(" · ") });
      }
    },
    [state],
  );

  const cancel = useCallback(() => {
    if (!activeId) return;
    // Stop drops whatever was queued behind the turn; the text comes back to
    // the composer rather than vanishing, in front of anything already typed.
    const queued = state?.queuedPrompts ?? [];
    if (queued.length > 0) {
      const restored = queued.map((q) => q.prompt).filter(Boolean);
      const current = drafts[activeId] ?? "";
      setDraft(activeId, [...restored, current].filter(Boolean).join("\n\n"));
    }
    clientRef.current?.command("cancel", { threadId: activeId }).catch((e) => {
      const message = e instanceof Error ? e.message : String(e);
      toast.error("Could not stop the turn", { description: message });
    });
  }, [activeId, drafts, setDraft, state]);

  const dequeue = useCallback(
    (queueId: string) => {
      if (!activeId) return;
      const text = state?.queuedPrompts?.find((q) => q.queueId === queueId)?.prompt ?? "";
      clientRef.current?.command("dequeue_prompt", { threadId: activeId, queueId }).then(
        () => {
          if (!text) return;
          // Against the draft as it is when the reply lands, not as it was
          // when the request left: on a slow link that is seconds apart.
          setDrafts((d) => ({ ...d, [activeId]: [text, d[activeId] ?? ""].filter(Boolean).join("\n\n") }));
        },
        (e) => toast.error("Could not remove that prompt", { description: e.message }),
      );
    },
    [activeId, state],
  );

  const resolvePermission = useCallback(
    (requestId: string, outcome: string, optionId: string) => {
      if (activeId) {
        clientRef.current?.command("resolve_permission", {
          threadId: activeId,
          requestId,
          outcome,
          optionId,
        });
      }
    },
    [activeId],
  );

  const resolveElicitation = useCallback(
    (requestId: string, action: string, value: unknown) => {
      if (activeId) {
        clientRef.current?.command("resolve_elicitation", {
          threadId: activeId,
          requestId,
          action,
          value,
        });
      }
    },
    [activeId],
  );

  // The returned promise settles when the server has *accepted* the delete,
  // not when it is done — the thread is gone when it leaves the list, which
  // is what the sidebar waits on. Rejecting it is the sidebar's cue to stop
  // waiting, so the error is re-thrown after it has been reported.
  const remove = useCallback(
    (id: string, removeWorktree: boolean) => {
      if (id !== activeRef.current) select(id);
      const client = clientRef.current;
      if (!client) {
        toast.error("Could not delete that thread", { description: "Not connected." });
        return Promise.reject(new Error("not connected"));
      }
      return client.command("delete_thread", { threadId: id, removeWorktree }).catch((e) => {
        toast.error("Could not delete that thread", { description: e.message });
        throw e;
      });
    },
    [select],
  );

  const forceDelete = useCallback((id: string) => {
    // Only a worktree omniplex provisioned is omniplex's to destroy, so only that case may
    // promise it. The old copy promised it to every thread and kept the
    // promise for one of them.
    const removes = threads.find((s) => s.id === id)?.workspaceMode === "managed";
    const accepted = window.confirm(
      removes
        ? "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script, removes the recorded Git worktree, and permanently deletes the thread."
        : "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script and permanently deletes the thread. The checkout is left on disk — omniplex did not create it.",
    );
    if (!accepted) return;
    clientRef.current?.command("force_delete_thread", { threadId: id }).catch((e) => toast.error("Force delete failed", { description: e.message }));
  }, [threads]);

  // Ask the server to re-probe, for when the user has just installed something.
  // The instance whose sign-in is open, if any. Closing it rechecks, so the
  // login shows up as "ready" by itself.
  const [loginInstance, setLoginInstance] = useState<string | null>(null);
  // The providers screen, and the structured sign-in dialog for one instance.
  const [authInstance, setAuthInstance] = useState<string | null>(null);

  const recheck = useCallback(() => {
    // Returned so a caller with a spinner can hold it up until the answer.
    return clientRef.current?.command("recheck_harnesses", {}).then((res) => {
      if (res?.harnesses) setHarnesses(res.harnesses);
    });
  }, []);

  // What the providers surface needs from the client: commands, and the
  // auth-flow event stream (which deliberately bypasses the state reducer —
  // flows are ephemeral and their frames can carry nothing persistable).
  const authWires = useMemo(
    () => ({
      command: (cmd: string, args: unknown) => clientRef.current!.command(cmd, args),
      subscribe: (flowId: string, listener: (ev: AuthFlowEvent) => void) =>
        clientRef.current?.onAuthFlow(flowId, listener) ?? (() => {}),
    }),
    [],
  );

  const meta = useMemo(() => threads.find((s) => s.id === activeId), [threads, activeId]);
  const activeProviderInstance = harnesses
    .flatMap((h) => h.instances ?? [])
    .find((i) => i.id === (meta?.providerInstance || meta?.harness));

  // Every "sign in" affordance routes through here: a flows-capable instance
  // gets the structured dialog, anything else the embedded login terminal.
  const openInstanceAuth = useCallback(
    (instanceId: string) => {
      const inst = harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === instanceId);
      if (inst?.auth === "flows") setAuthInstance(instanceId);
      else setLoginInstance(instanceId);
    },
    [harnesses],
  );

  // The empty transcript's list of skills to reach for, and the composer it
  // writes into. Both live up here for the same reason the drafts do: the
  // Transcript and the Composer are siblings remounted per thread, and this
  // is the one place that can see the catalogue, the project, and the input at
  // once.
  const composerRef = useRef<ComposerHandle>(null);
  const [recents, setRecents] = useState<{ items: ComposerItem[]; seeded: boolean }>({
    items: [],
    seeded: false,
  });
  const projectId = meta?.projectId;
  // Only an empty transcript asks for this, so only an empty transcript pays
  // for the catalogue fetch. A newly provisioned thread publishes empty
  // snapshots before its harness is ready; asking during those snapshots can
  // produce an empty catalogue that would otherwise stick until a thread
  // switch. Wait for the harness-backed idle/turn phase instead.
  const transcriptEmpty =
    !!state && state.items.length === 0 && (state.phase === "idle" || state.phase === "turn");
  useEffect(() => {
    if (!activeId || !transcriptEmpty) {
      setRecents((prev) => (prev.items.length === 0 ? prev : { items: [], seeded: false }));
      return;
    }
    let cancelled = false;
    loadComposerItems()
      .then((catalogue) => {
        if (cancelled) return;
        const history = loadRecentSkills(projectId);
        const items = resolveRecentSkills(history, catalogue);
        // Seeded means none of what is being shown was actually remembered —
        // a first run, or a project whose history no longer resolves.
        const seeded = !items.some((item) => history.includes(item.insertText));
        setRecents({ items, seeded });
      })
      .catch(() => {
        // No catalogue, no suggestions. The empty state still reads fine.
      });
    return () => {
      cancelled = true;
    };
  }, [activeId, transcriptEmpty, loadComposerItems, projectId]);

  // Clicking a suggestion writes the token and a space into the draft, and
  // nothing else: what happens next — an argument, or straight to submit — is
  // the user's to decide. Desktop takes the cursor with it, because the next
  // keystroke almost always belongs in the input. A phone deliberately does
  // not: focusing raises the keyboard over the very button just tapped, and
  // submit is one tap away without it.
  const pickRecent = useCallback(
    (item: ComposerItem) => {
      if (!activeId) return;
      const current = drafts[activeId] ?? "";
      const prefix = current && !/\s$/.test(current) ? `${current} ` : current;
      const next = `${prefix}${item.insertText} `;
      setDraft(activeId, next);
      if (isDesktop) composerRef.current?.focusEnd(next.length);
    },
    [activeId, drafts, isDesktop, setDraft],
  );

  const noteSkillUsed = useCallback(
    (insertText: string) => recordRecentSkill(projectId, insertText),
    [projectId],
  );

  // Whether the work in this thread has landed, and the confirmation the
  // transcript's prompt opens. The dialog and its guards are the sidebar's
  // own, so "finish with this thread" and the row's X are the same action
  // reached from two places; only the sidebar's row animation is not shared,
  // because the transcript has no row.
  const deleteFlow = useDeleteThread({
    threads,
    onDelete: remove,
    projectFolders: (id) => projects.find((p) => p.id === id)?.folders.map((f) => f.path) ?? [],
  });
  const fetchPR = useCallback(async (threadId: string): Promise<PullRequest | null> => {
    const res = await clientRef.current!.command("thread_pr", { threadId });
    return (res.pr ?? null) as PullRequest | null;
  }, []);
  // The server checks this too and is the authority; asking here only spares
  // a subprocess for the threads that plainly have nothing to report.
  const prEligible =
    (meta?.workspaceMode === "managed" || meta?.workspaceMode === "borrowed") && !!meta?.branch;
  const pr = useThreadPR(activeId, prEligible, fetchPR);

  // The permission modes for the attached thread's harness. Everything the UI
  // knows about them came from the adapter via the server; ids stay opaque.
  const modeOptions = useMemo(
    () => harnesses.find((h) => h.id === state?.harness)?.permissionModes ?? [],
    [harnesses, state?.harness],
  );
  // An empty recorded mode means the harness default; render it as such.
  const currentModeId =
    (modeOptions.some((m) => m.id === state?.mode) ? state?.mode : undefined) ??
    modeOptions.find((m) => m.default)?.id ??
    modeOptions[0]?.id ??
    "";

  const switchMode = useCallback(
    (modeId: string) => {
      if (!activeId) return;
      // Every mode switches the same way: the picked value is the decision.
      clientRef.current?.command("set_mode", { threadId: activeId, mode: modeId }).catch((e) => {
        toast.error("Could not switch permission mode", { description: e.message });
      });
    },
    [activeId],
  );
  const accentOf = useCallback(
    (id: string) => harnesses.find((h) => h.id === id)?.accent,
    [harnesses],
  );

  const switchModel = useCallback(
    (modelId: string) => {
      if (!activeId) return;
      clientRef.current?.command("set_model", { threadId: activeId, model: modelId }).catch((e) => {
        toast.error("Could not switch model", { description: e.message });
      });
    },
    [activeId],
  );
  const switchEffort = useCallback(
    (effort: string) => {
      if (!activeId) return;
      clientRef.current?.command("set_effort", { threadId: activeId, effort }).catch((e) => {
        toast.error("Could not change reasoning effort", { description: e.message });
      });
    },
    [activeId],
  );
  // Re-sends a failed turn's prompt, images and all. Only ever on an explicit
  // press: nothing finishing — a sign-in, an account switch — resends by itself.
  const retryTurn = useCallback(
    (turn: Turn) => {
      if (!activeId) return;
      clientRef.current
        ?.command("prompt", {
          threadId: activeId,
          text: turn.prompt,
          ...(turn.images?.length ? { imageIds: turn.images.map((i) => i.id) } : {}),
        })
        .catch((e) => toast.error("Could not send", { description: e.message }));
    },
    [activeId],
  );
  // The thread's harness's other accounts that could take the next turn: the
  // way out of a usage limit.
  const activeInstanceId = activeProviderInstance?.id;
  const switchTargets = useMemo(
    () =>
      (harnesses.find((h) => h.id === state?.harness)?.instances ?? [])
        .filter((i) => i.enabled !== false && i.availability?.state === "ready" && i.id !== activeInstanceId)
        .map((i) => ({ id: i.id, name: i.displayName })),
    [harnesses, state?.harness, activeInstanceId],
  );
  // Moves the thread, conversation and all, to another account of its
  // harness. From the model picker it asks first — a picker row is an easy
  // thing to tap by accident — and may bring a model along; from the limit
  // card, where the button says exactly what it does, it goes straight on to
  // retry the prompt that hit the limit.
  const switchAccount = useCallback(
    async (instance: string, opts: { model?: string; retry?: Turn; confirm?: boolean } = {}): Promise<boolean> => {
      if (!activeId || !clientRef.current) return false;
      const name =
        harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === instance)?.displayName ?? instance;
      if (
        opts.confirm &&
        !window.confirm(`Move this thread to ${name}?\n\nThe conversation comes with it; the next turn runs on ${name}.`)
      ) {
        return false;
      }
      try {
        await clientRef.current.command("switch_account", { threadId: activeId, instance });
      } catch (e) {
        toast.error("Could not switch account", { description: (e as Error).message });
        return false;
      }
      if (opts.model && opts.model !== state?.model) switchModel(opts.model);
      if (opts.retry) retryTurn(opts.retry);
      return true;
    },
    [activeId, harnesses, state?.model, switchModel, retryTurn],
  );
  const pending = state?.pendingPermissions?.[0];
  const elicitation = state?.pendingElicitations?.[0];
  // The tab is named after whatever is attached, so a phone with several
  // threads open in several tabs can tell them apart without switching to
  // each one.
  // The list entry, not just the attached state: switching threads drops
  // `state` until the snapshot lands, and on a slow connection that would
  // leave every tab called "Omniplex" for exactly as long as it takes to
  // reconnect — which is when telling them apart matters most.
  useDocumentTitle(
    activeId
      ? { title: state?.title ?? meta?.title, needsAttention: Boolean(pending || elicitation) }
      : null,
  );
  // Only a project with no threads can be deleted; settings says so up front.
  const threadCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const t of threads) if (t.projectId) counts[t.projectId] = (counts[t.projectId] ?? 0) + 1;
    return counts;
  }, [threads]);
  // Preparing is not closed: the worktree is still being cut, but the user can
  // already write the first message — only sending waits. Cleaning is different:
  // the workspace is going away, so there is nothing left to write to.
  const workspacePreparing = state ? ["creating","provisioning"].includes(state.phase) : false;
  const workspaceCleaning = state?.phase === "cleaning";
  const workspaceBusy = workspacePreparing || workspaceCleaning;
  const workspaceFailed = state ? ["provision_failed","cleanup_failed"].includes(state.phase) : false;

  useEffect(() => {
    if (!activeId || state?.phase !== "cleanup_failed" || !state.workspace.deleteAfterCleanup) return;
    const key = `${activeId}:${state.seq}`;
    if (forcePromptedRef.current === key) return;
    forcePromptedRef.current = key;
    forceDelete(activeId);
  }, [activeId, state, forceDelete]);

  // The attached thread went away (deleted elsewhere, or torn down here).
  //
  // "Absent from the list" only means gone if it was ever in the list: a
  // thread we just created is attached before the broadcast carrying it
  // arrives, and treating that gap as a disappearance would detach the
  // thread the user is watching being born. So it has to have been seen
  // first. Waiting for `state` instead would be the wrong test — deleting a
  // row that is not the open one selects it first, which clears state, so a
  // delete landing before the first snapshot would leave the app attached to
  // nothing and stuck on "Attaching…" forever.
  //
  // On a phone this also leaves nothing behind the sidebar, so it comes back.
  const seenActive = useRef<string | null>(null);
  useEffect(() => {
    if (!activeId) return;
    if (threads.some((s) => s.id === activeId)) {
      seenActive.current = activeId;
      return;
    }
    if (seenActive.current !== activeId) return;
    seenActive.current = null;
    setActiveId(null); setState(null); clientRef.current?.detach();
    if (!isDesktop) setSidebarOpen(true);
  }, [threads, activeId, isDesktop]);

  // Drop drafts for threads that have left the list, so a deleted thread does
  // not leave its text behind for the life of the tab. "Absent from the list"
  // only means gone if the thread was ever *in* the list: a freshly created
  // thread is attached — and can be typed into — before the broadcast listing
  // it arrives, and treating that gap as a disappearance would prune its draft.
  // Same reasoning, and the same guard, as `seenActive` above.
  const seenThreads = useRef<Set<string>>(new Set());
  useEffect(() => {
    for (const s of threads) seenThreads.current.add(s.id);
    // The scroll positions go the same way, and for the same reason: a
    // deleted thread's offset means nothing, and a new id reusing it would
    // be handed a stranger's place in the transcript.
    for (const s of threads) goneThreads.current.delete(s.id);
    for (const id of Object.keys(scrollPositions.current)) {
      if (threads.some((s) => s.id === id) || !seenThreads.current.has(id)) continue;
      delete scrollPositions.current[id];
      goneThreads.current.add(id);
    }
    setDrafts((d) => {
      const live = new Set(threads.map((s) => s.id));
      const next: Record<string, string> = {};
      let changed = false;
      for (const [id, text] of Object.entries(d)) {
        if (live.has(id) || !seenThreads.current.has(id)) next[id] = text;
        else changed = true;
      }
      return changed ? next : d;
    });
    // Staged images go the same way, releasing their preview URLs as they do:
    // a deleted thread must not leak blobs for the life of the tab.
    setAttachments((all) => {
      const live = new Set(threads.map((s) => s.id));
      const next: Record<string, Attachment[]> = {};
      let changed = false;
      for (const [id, list] of Object.entries(all)) {
        if (live.has(id) || !seenThreads.current.has(id)) next[id] = list;
        else {
          for (const a of list) URL.revokeObjectURL(a.previewUrl);
          changed = true;
        }
      }
      return changed ? next : all;
    });
  }, [threads]);

  // Nothing measures the composer on its own, so a fixed padding could only
  // ever guess at its height — and it grows (a tall draft, a permission prompt
  // appearing above it) well past any guess. A ResizeObserver on the whole
  // overlay keeps `--composer-h` exactly right, and the transcript reserves
  // `that + headroom` below its tail. Grow the overlay and the content above it
  // visibly rises: it reads as the composer pushing the transcript up, even
  // though it is floating.
  const hasThread = state != null;
  // The resume cache is one boot's worth of help. Its scroll position moved
  // into the per-thread map above as the page hydrated, and the transcript
  // takes over reporting from there, so once a thread is on screen the blob
  // has done its job.
  useEffect(() => {
    if (resume && hasThread) setResume(null);
  }, [resume, hasThread]);
  //
  // The same observer publishes the transcript's scrollbar width as
  // `--scrollbar-w`, so the fades and the composer stop short of the scrollbar
  // instead of painting over it. It is 0 on a phone, whose scrollbars float, and
  // whatever the browser makes it on a desktop. Observing the scroller's content
  // box catches the scrollbar coming and going as the transcript grows.
  useEffect(() => {
    if (!hasThread || typeof ResizeObserver === "undefined") return;
    const overlay = overlayRef.current;
    const layout = chatLayoutRef.current;
    const scroller = layout?.querySelector<HTMLElement>("[data-transcript-scroller]");
    if (!overlay || !layout) return;
    const apply = () => {
      layout.style.setProperty(
        "--composer-h",
        `${Math.ceil(overlay.getBoundingClientRect().height)}px`,
      );
      layout.style.setProperty(
        "--scrollbar-w",
        `${scroller ? scroller.offsetWidth - scroller.clientWidth : 0}px`,
      );
    };
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(overlay);
    if (scroller) ro.observe(scroller);
    return () => {
      ro.disconnect();
      layout.style.removeProperty("--composer-h");
      layout.style.removeProperty("--scrollbar-w");
    };
    // themePreview toggles the whole main tree in and out below, so the
    // measured elements are remounted under it: re-run to observe the new ones.
    // The transcript is keyed by thread, so a new thread is a new scroller.
  }, [hasThread, themePreview, activeId]);

  // Historical usage: the server aggregates the event log and prices it, so
  // the phone only ever downloads the bucketed result.
  const loadUsageReport = useCallback(async (range: string): Promise<UsageReport> => {
    const res = await clientRef.current!.command("usage_report", { range });
    return res.report as UsageReport;
  }, []);
  const refreshQuota = useCallback(async (instance: string): Promise<QuotaStatus[]> => {
    const res = await clientRef.current!.command("quota_refresh", { instance });
    return (res.quotas ?? []) as QuotaStatus[];
  }, []);

  if (themePreview) return <Suspense fallback={<div className="flex h-dvh items-center justify-center"><Spinner /></div>}><ThemePreview /></Suspense>;

  // The Usage page covers the whole viewport, above everything: it answers an
  // account question, and the thread underneath keeps streaming while it is
  // up.
  if (showUsage) {
    return (
      <Suspense fallback={<div className="flex h-dvh items-center justify-center"><Spinner /></div>}>
        <UsagePage
          quotas={quotas}
          onRefreshQuota={refreshQuota}
          loadReport={loadUsageReport}
          onClose={() => setShowUsage(false)}
        />
      </Suspense>
    );
  }

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar
        threads={threads}
        activeId={activeId}
        status={status}
        open={sidebarOpen}
        onOpenChange={setSidebarOpen}
        onSelect={select}
        onNew={startNew}
        onDelete={remove}
        onShowAccess={() => setShowAccess(true)}
        onShowUsage={() => setShowUsage(true)}
        onShowSettings={() => setSettings({})}
        accentOf={accentOf}
        projects={projects}
        projectName={(id)=>projects.find(p=>p.id===id)?.name}
        projectFolders={(id)=>projects.find(p=>p.id===id)?.folders.map((f)=>f.path) ?? []}
        labels={labels}
        onSetLabel={setThreadLabel}
        onManageLabels={openLabelManager}
        onNewProject={() => setNewProject(true)}
        onSetUnread={setThreadUnread}
      />

      <DeleteThreadDialog flow={deleteFlow} />

      <main
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col",
          // The expanded diff panel takes the whole content area; the main
          // column stays mounted so the transcript keeps its scroll and state.
          showChanges && changesExpanded && "hidden",
        )}
      >
        <header className="flex items-center gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-3">
          {/* The open sidebar carries its own collapse button, so this one
              only appears when there is a closed sidebar to reopen. */}
          <IconButton
            label="Show threads"
            onClick={() => setSidebarOpen(true)}
            className={cn(sidebarOpen && "hidden")}
          >
            <PanelLeftIcon />
          </IconButton>

          {state ? (
            <>
              <p className="min-w-0 flex-1 truncate text-[13px] font-medium">
                {state.title || "Untitled thread"}
              </p>

              {SHOW_MODE_SWITCHER && modeOptions.length > 0 && !state.closed && (
                <Select value={currentModeId} onValueChange={switchMode}>
                  {/* Every mode gets the same chip: one that changed shape or
                      colour by mode would jitter the header and shout at the
                      user about a choice they already made deliberately. */}
                  <SelectTrigger
                    aria-label="Permission mode"
                    className="h-8 w-auto shrink-0 gap-1 px-2 text-[11px]"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {modeOptions.map((m) => (
                      <SelectItem key={m.id} value={m.id}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}

              {/* Filing the open thread — the same menu the sidebar row
                  carries, so a thread can be labelled from either place.
                  Invisible until the user has defined a label. */}
              {isDesktop && labels.length > 0 && activeId && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    {(() => {
                      const current = labels.find((l) => l.id === meta?.labelId);
                      return current ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Labelled ${current.name} — change label`}
                          className="text-muted-foreground h-8 max-w-32 gap-1.5 px-2 text-[11px]"
                        >
                          <LabelDot color={current.color} />
                          <span className="truncate">{current.name}</span>
                        </Button>
                      ) : (
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label="Label this thread"
                          className="size-8"
                        >
                          <TagIcon />
                        </Button>
                      );
                    })()}
                  </DropdownMenuTrigger>
                  <LabelMenu
                    labels={labels}
                    current={meta?.labelId}
                    onSelect={(labelId) => setThreadLabel(activeId, labelId)}
                    onManage={openLabelManager}
                  />
                </DropdownMenu>
              )}

              {isDesktop ? (
                <>
                  <IconButton
                    label={transcriptCopied ? "Transcript copied" : "Copy transcript"}
                    onClick={() => void copyFullTranscript()}
                  >
                    {transcriptCopied ? <CheckIcon className="text-success" /> : <CopyIcon />}
                  </IconButton>

                  <IconButton
                    label={showChanges ? "Hide the panel" : "Show the panel"}
                    onClick={() => setShowChanges((v) => !v)}
                    className={cn("relative", showChanges && "bg-accent")}
                  >
                    <PanelRightIcon />
                    {/* A live agent-count badge: work is happening off-transcript. */}
                    {liveJobCount(state.jobs) > 0 && (
                      <span className="bg-primary text-primary-foreground absolute -top-0.5 -right-0.5 flex size-3.5 items-center justify-center rounded-full text-[9px] tabular-nums">
                        {liveJobCount(state.jobs)}
                      </span>
                    )}
                  </IconButton>
                </>
              ) : (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="More thread actions"
                      className="relative size-11 shrink-0"
                    >
                      <EllipsisIcon />
                      {liveJobCount(state.jobs) > 0 && (
                        <span className="bg-primary text-primary-foreground absolute top-0.5 right-0.5 flex size-3.5 items-center justify-center rounded-full text-[9px] tabular-nums">
                          {liveJobCount(state.jobs)}
                        </span>
                      )}
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="min-w-48">
                    <DropdownMenuItem onSelect={() => setShowChanges(true)}>
                      <PanelRightIcon /> Open panel
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      onSelect={() => void copyFullTranscript()}
                    >
                      {transcriptCopied ? <CheckIcon className="text-success" /> : <CopyIcon />}
                      {transcriptCopied ? "Transcript copied" : "Copy transcript"}
                    </DropdownMenuItem>
                    {labels.length > 0 && activeId && (
                      <>
                        <DropdownMenuSeparator />
                        <DropdownMenuSub>
                          <DropdownMenuSubTrigger>
                            {(() => {
                              const current = labels.find((l) => l.id === meta?.labelId);
                              return current ? (
                                <>
                                  <LabelDot color={current.color} />
                                  <span className="truncate">{current.name}</span>
                                </>
                              ) : (
                                <>
                                  <TagIcon /> Label thread
                                </>
                              );
                            })()}
                          </DropdownMenuSubTrigger>
                          <DropdownMenuSubContent className="min-w-40">
                            <LabelMenuItems
                              labels={labels}
                              current={meta?.labelId}
                              onSelect={(labelId) => setThreadLabel(activeId, labelId)}
                              onManage={openLabelManager}
                            />
                          </DropdownMenuSubContent>
                        </DropdownMenuSub>
                      </>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}

            </>
          ) : (
            <>
              <span
                className={cn(
                  "flex-1 text-[13px]",
                  creating && !meta ? "font-medium" : "text-muted-foreground",
                )}
              >
                {meta ? "Attaching…" : creating ? "New thread" : ""}
              </span>
            </>
          )}
        </header>

        {state ? (
          <div ref={chatLayoutRef} className="relative flex min-h-0 flex-1 flex-col">
            {/* Content scrolling up dissolves into the header rather than
                being cut by a border. */}
            <div className="from-background to-background/0 pointer-events-none absolute top-0 right-(--scrollbar-w,0px) left-0 z-10 h-8 bg-gradient-to-b" />

            <OpenPathContext.Provider value={openPath}>
              <Transcript key={activeId} state={state} hasOlder={(state.itemsBefore ?? 0) > 0} onLoadOlder={loadOlderItems} initialScroll={activeId ? scrollPositions.current[activeId] : undefined} onScrollChange={recordScroll} onContinue={()=>activeId&&clientRef.current?.command("continue_thread",{threadId:activeId})} onLogin={activeProviderInstance?.canLogin ? ()=>openInstanceAuth(activeProviderInstance.id) : undefined} providerName={activeProviderInstance?.displayName} providerReady={activeProviderInstance?.availability.state === "ready"} onRetryTurn={retryTurn} switchTargets={switchTargets} onSwitchAccount={(instance, retry) => switchAccount(instance, { retry })} onRetryProvision={()=>activeId&&clientRef.current?.command("retry_provision",{threadId:activeId})} onCleanup={()=>activeId&&clientRef.current?.command("cleanup_thread",{threadId:activeId})} onForceDelete={()=>activeId&&forceDelete(activeId)} onOpenDiff={openDiff} jobs={state.jobs} onOpenJobs={openJobs} onOpenArtefact={openArtefact} pr={pr} onFinish={()=>meta&&deleteFlow.ask(meta)} recents={recents.items} recentsSeeded={recents.seeded} onPickRecent={pickRecent} onDequeue={dequeue} />
            </OpenPathContext.Provider>

            {/* The mirror of the header fade: content dissolves into the
                composer instead of sliding under a hard edge. It sits just
                above the overlay, tracking its measured height. */}
            <div
              className="from-background to-background/0 pointer-events-none absolute right-(--scrollbar-w,0px) left-0 z-10 h-8 bg-gradient-to-t"
              style={{ bottom: "var(--composer-h, 9rem)" }}
            />

            {/* The input floats over the transcript's tail instead of sitting
                in a full-width tray. Anything that blocks the turn — a
                permission or elicitation — stacks above it. It is opaque: the
                fade above ends in solid background, and a see-through overlay
                let text show again at full strength right under that edge. */}
            <div ref={overlayRef} className="bg-background absolute right-(--scrollbar-w,0px) bottom-0 left-0 z-10">
              {pending && (
                <PermissionPrompt
                  request={pending}
                  onResolve={(outcome, optionId) =>
                    resolvePermission(pending.requestId, outcome, optionId)
                  }
                />
              )}

              {elicitation && (
                <ElicitationPrompt
                  request={elicitation}
                  onResolve={(action, value) => resolveElicitation(elicitation.requestId, action, value)}
                />
              )}

              {liveJobCount(state.jobs) > 0 && <JobsStrip jobs={state.jobs} onOpen={openJobs} />}

              <ScheduledPrompts schedules={state.scheduledPrompts ?? []} disabled={state.closed || workspaceBusy || workspaceFailed} onEdit={p => activeId && setScheduleEditor({id:uuid(),threadId:activeId,text:p.prompt,imageIds:(p.images ?? []).map(i=>i.id),schedule:p})} onAction={async (action,p) => {if(!clientRef.current)throw new Error("Reconnect first");await clientRef.current.command(action,{threadId:activeId,id:p.id,revision:p.revision});}} />
              {scheduleEditor && <ScheduleDialog key={`schedule:${scheduleEditor.id}`} initialText={scheduleEditor.text} imageCount={scheduleEditor.imageIds.length} schedule={scheduleEditor.schedule} onClose={()=>setScheduleEditor(null)} onSave={saveSchedule} />}
              <Composer
                key={activeId}
                ref={composerRef}
                draft={activeId ? (drafts[activeId] ?? "") : ""}
                onDraftChange={(text) => activeId && setDraft(activeId, text)}
                disabled={state.closed || workspaceCleaning || workspaceFailed}
                disabledPlaceholder={workspaceBusy ? (workspaceCleaning ? "Cleaning up workspace…" : "Preparing workspace…") : workspaceFailed ? "Workspace needs attention" : undefined}
                busy={state.phase === "turn"}
                onSend={send}
                onSchedule={()=>activeId && setScheduleEditor({id:uuid(),threadId:activeId,text:drafts[activeId] ?? "",imageIds:sendPayload(attachments[activeId] ?? []).imageIds})}
                onCancel={cancel}
                attachments={activeId ? (attachments[activeId] ?? []) : []}
                onAttachImages={attachImages}
                onRemoveAttachment={(key) => activeId && removeAttachment(activeId, key)}
                harnesses={harnesses}
                harness={state.harness}
                instance={meta?.providerInstance ?? ""}
                model={state.model}
                effort={state.effort}
                onSwitchModel={switchModel}
                onSwitchEffort={switchEffort}
                onSwitchAccount={(instance, model) => void switchAccount(instance, { model, confirm: true })}
                usage={state.usage}
                loadComposerItems={loadComposerItems}
                onRunClientAction={runClientComposerAction}
                onRunComposerAction={runComposerAction}
                onCommandUsed={noteSkillUsed}
              />
            </div>
          </div>
        ) : creating ? (
          <ThreadDraft
            projects={projects}
            activeProjectId={creating.projectId}
            harnesses={harnesses}
            userConfig={userConfig}
            status={status}
            draft={drafts[NEW_THREAD] ?? ""}
            onDraftChange={(text) => setDraft(NEW_THREAD, text)}
            onStart={create}
            attachments={attachments[NEW_THREAD] ?? []}
            onAttachImages={attachToDraft}
            onRemoveAttachment={(key) => removeAttachment(NEW_THREAD, key)}
            onListWorkspaces={listWorkspaces}
            onListIssues={listIssues}
            onAddProject={() => setNewProject(true)}
            onSettings={(p) => setSettings({ at: { kind: "project", id: p.id } })}
            onRecheck={recheck}
            onLogin={openInstanceAuth}
            onManageProviders={() => setSettings({ at: { kind: "providers" } })}
          />
        ) : (
          <EmptyState
            restoring={restoring}
            attaching={!!activeId}
            hasThreads={threads.length > 0}
            onNew={startNew}
          />
        )}
      </main>

      {state && activeId && panelLoaded && (
        <Suspense fallback={null}>
          <Panel
          // Remounted per thread: the tab model is per-thread state.
          key={activeId}
          threadId={activeId}
          state={state}
          command={panelCommand}
          open={showChanges}
          onClose={() => { setShowChanges(false); setChangesExpanded(false); }}
          expanded={changesExpanded}
          onToggleExpanded={() => setChangesExpanded((v) => !v)}
          // The worktree is worth re-reading when the agent stops writing to it.
          revision={`${activeId}:${state.phase === "turn" ? "turn" : "settled"}`}
          loadChanges={loadChanges}
          loadDiff={loadFileDiff}
          loadTree={loadFileTree}
          loadFile={loadFile}
          request={panelRequest}
          pr={pr}
          />
        </Suspense>
      )}

      {manageLabels && (
        <LabelManager
          labels={labels}
          onCreate={createLabel}
          onSave={saveLabel}
          onDelete={deleteLabel}
          onClose={() => setManageLabels(false)}
        />
      )}

      {showAccess && access && (
        <AccessPanel
          access={access}
          onEnableHTTPS={async () => {
            const res = await clientRef.current!.command("enable_https", {});
            if (res?.access) setAccess(res.access);
          }}
          onDisableHTTPS={async () => {
            const res = await clientRef.current!.command("disable_https", {});
            if (res?.access) setAccess(res.access);
          }}
          onClose={() => setShowAccess(false)}
        />
      )}

      {authInstance && (
        <Suspense fallback={null}>
          <InstanceAuthDialog
            wires={authWires}
            instanceId={authInstance}
            instanceName={
              harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === authInstance)?.displayName ??
              authInstance
            }
            onOpenTerminal={() => {
              setAuthInstance(null);
              setLoginInstance(authInstance);
            }}
            onClose={() => setAuthInstance(null)}
          />
        </Suspense>
      )}
      {loginInstance && (
        <Suspense fallback={null}>
          <LoginDialog
            instanceId={loginInstance}
            name={
              harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === loginInstance)?.displayName ??
              loginInstance
            }
            onEnded={recheck}
            onClose={() => {
              setLoginInstance(null);
              recheck();
            }}
          />
        </Suspense>
      )}
      {newProject && (
        <Suspense fallback={null}>
          <NewProject
            onCreate={createProject}
            listRepos={listRepos}
            onClose={() => setNewProject(false)}
          />
        </Suspense>
      )}
      {settings && (
        <Suspense fallback={null}>
          <SettingsScreen
            at={settings.at}
            projects={projects}
            harnesses={harnesses}
            userConfig={userConfig}
            threadCounts={threadCounts}
            onSaveUserConfig={saveUserConfig}
            providers={{ wires: authWires, onOpenTerminal: setLoginInstance, onRecheck: recheck }}
            project={{
              onSave: saveProject,
              onAddFolder: addFolder,
              onRemoveFolder: removeFolder,
              listRepos,
              onDelete: deleteProject,
            }}
            onAddProject={() => {
              setSettings(null);
              setNewProject(true);
            }}
            onClose={() => setSettings(null)}
          />
        </Suspense>
      )}
    </div>
  );
}

/**
 * What the content column shows with nothing attached.
 *
 * There are three of these and they are genuinely different situations, so
 * they say different things. A single oversized "New thread" button was
 * answering all three with a call to action nobody asked for — on a phone it
 * was the whole landing screen, and on a desktop with threads in the list it
 * was pointing away from them.
 */
function EmptyState({
  restoring,
  attaching,
  hasThreads,
  onNew,
}: {
  restoring: boolean;
  attaching: boolean;
  hasThreads: boolean;
  onNew: () => void;
}) {
  // Mid-restore. Saying anything here would only be contradicted a moment
  // later, so it says nothing and just holds the space.
  if (restoring) {
    return (
      <div className="flex flex-1 items-center justify-center" aria-busy="true">
        <span className="sr-only">Reopening your last thread…</span>
        <Spinner className="text-muted-foreground/60 size-5" />
      </div>
    );
  }

  // Selecting clears the old snapshot before attaching to the new thread.
  // That gap is loading, not an invitation to create another thread.
  if (attaching) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 pb-16" aria-busy="true">
        <Spinner className="text-muted-foreground/60 size-6" />
        <p className="text-muted-foreground text-[13px]">Attaching to thread…</p>
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 pb-16 text-center">
      {hasThreads ? (
        <>
          <MessagesSquareIcon aria-hidden className="text-muted-foreground/40 size-7" />
          <div className="max-w-xs">
            <p className="text-[15px] font-medium">Nothing open</p>
            <p className="text-muted-foreground mt-1.5 text-[13px] leading-relaxed">
              Pick a thread from the list to jump back into it.
            </p>
          </div>
        </>
      ) : (
        <>
          <CoffeeIcon aria-hidden className="text-muted-foreground/40 size-7" />
          <div className="max-w-xs">
            <p className="text-[15px] font-medium">All caught up</p>
            <p className="text-muted-foreground mt-1.5 text-[13px] leading-relaxed">
              Nothing is running. Put your feet up — or start something new.
            </p>
          </div>
        </>
      )}
      {/* Offered, not insisted on — but still a real target for a thumb. */}
      <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={onNew}>
        <PlusIcon />
        New thread
      </Button>
    </div>
  );
}
