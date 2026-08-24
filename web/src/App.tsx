import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Client, uuid, wsURL, type ConnectionStatus } from "./client";
import { useIsDesktop } from "./useMediaQuery";
import { useDocumentTitle } from "./useDocumentTitle";
import { useSessionPR } from "./useSessionPR";
import type { Access, ComposerItem, FileContent, FileDiff, FileTree, HarnessMeta, Label, Project, ProjectConfig, SessionChanges, SessionMeta, SessionState, SessionSummary, PullRequest, UserConfig, Workspace } from "./protocol";
import { AccessPanel } from "./components/Access";
import type { PanelRequest } from "./components/panel/Panel";
import { liveAgentCount } from "./lib/agents";
import { OpenPathContext } from "./lib/openPath";
import { Composer, type ComposerHandle } from "./components/Composer";
import { NewSession } from "./components/NewSession";
import type { NewSessionInput } from "./components/NewSession";
import { PermissionPrompt } from "./components/PermissionPrompt";
import { ElicitationPrompt } from "./components/ElicitationPrompt";
import { DeleteSessionDialog, useDeleteSession } from "./components/DeleteSessionDialog";
import { LabelManager } from "./components/LabelManager";
import { LabelDot, LabelMenu } from "./components/LabelMenu";
import { DropdownMenu, DropdownMenuTrigger } from "./components/ui/dropdown-menu";
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
import { isSupportedImage, MAX_IMAGE_BYTES, prepareImage, uploadAttachment, type Attachment } from "./lib/attachments";
import { loadRecentSkills, recordRecentSkill, resolveRecentSkills } from "./lib/recentSkills";
import { loadResume } from "./resume";
import { cn } from "./lib/utils";
import { transcriptMarkdown } from "./lib/transcript";
import { useCopy } from "./lib/clipboard";
import {
  CheckIcon,
  CoffeeIcon,
  CopyIcon,
  FileDiffIcon,
  MessagesSquareIcon,
  PanelLeftIcon,
  PlusIcon,
  SettingsIcon,
  SparklesIcon,
  TagIcon,
} from "lucide-react";
import { toast } from "sonner";

const LAST_SESSION = "omniplex.lastSession";

const Panel = lazy(() => import("./components/panel/Panel").then((m) => ({ default: m.Panel })));
const SessionSummaryPanel = lazy(() => import("./components/SessionSummary").then((m) => ({ default: m.SessionSummaryPanel })));
const ProjectSettings = lazy(() => import("./components/ProjectSettings").then((m) => ({ default: m.ProjectSettings })));
const ThemePreview = lazy(() => import("./components/ThemePreview").then((m) => ({ default: m.ThemePreview })));

// The permission-mode switcher is parked, not removed: changing modes mid-chat
// is not something we want to offer right now, and hiding it is cheaper to
// reverse than deleting it. Flip this to bring it back.
const SHOW_MODE_SWITCHER = false;

export function App() {
  const { copied: transcriptCopied, copy: copyTranscript } = useCopy();
  // The snapshot a previous page of this tab saved as it went to background
  // (resume.ts). A mobile browser discards a backgrounded tab and reloads it
  // on return; hydrating from the cache paints the session as it was left —
  // right frame one, right scroll position — instead of "Attaching…", and the
  // socket then fetches only what the page missed. Cleared once consumed, and
  // if the session turns out to be gone when the list arrives.
  const [resume, setResume] = useState(() => {
    try {
      return loadResume(localStorage.getItem(LAST_SESSION));
    } catch {
      return null;
    }
  });
  // The copy the socket-setup effect reads: priming must use what the page
  // hydrated with, not what later clearing left behind.
  const resumeRef = useRef(resume);
  // The socket callbacks below outlive any single render, so they read the
  // attached session from a ref rather than a captured closure. The ref is
  // written after commit, never during render, so it can only ever hold a
  // value the UI actually rendered.
  const activeRef = useRef<string | null>(resume?.state.sessionId ?? null);
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [harnesses, setHarnesses] = useState<HarnessMeta[]>([]);
  const [defaultCwd, setDefaultCwd] = useState("");
  const [projects, setProjects] = useState<Project[]>([]);
  // The user's label definitions, server-owned: every mutation round-trips
  // and comes back as a broadcast, so paired devices all render the same set.
  const [labels, setLabels] = useState<Label[]>([]);
  const [manageLabels, setManageLabels] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(resume?.state.sessionId ?? null);
  const [state, setState] = useState<SessionState | null>(resume?.state ?? null);
  // Read by long-lived callbacks (openPath) that must see the current state
  // without re-creating themselves on every event.
  const stateRef = useRef<SessionState | null>(resume?.state ?? null);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);
  // Composer drafts, kept per session up here rather than inside the Composer.
  // Switching sessions nulls `state`, which unmounts the whole content subtree
  // (Composer included) and remounts it for the next session — so a draft owned
  // by the Composer would be destroyed on every switch. Holding it in the
  // parent, keyed by session id, lets a half-typed message survive the swap and
  // still be there when you come back. Session scope only: no persistence, and
  // the map is pruned as sessions go away (see below).
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // Images staged for the next message, per session and for the same reason as
  // the drafts: switching away and back must not lose what you attached. The
  // upload starts as soon as a picture is picked, so by send time this is a
  // list of ids the server already holds.
  const [attachments, setAttachments] = useState<Record<string, Attachment[]>>({});
  const [composerRevision, setComposerRevision] = useState(0);
  // Where each session's transcript was scrolled, kept up here for the same
  // reason as the drafts: switching sessions unmounts the Transcript, so a
  // position it owned would be lost every time — you would come back to a
  // session you were reading half-way up and find yourself at the bottom.
  // A ref rather than state: the transcript reports every scroll, and nothing
  // on the page renders from this, so re-rendering the app on each one would
  // be pure cost. Seeded from the resume cache so the boot restore and the
  // switch restore are one path. Session scope only, pruned with the drafts.
  const scrollPositions = useRef<Record<string, { top: number; atBottom: boolean }>>(
    resume ? { [resume.state.sessionId]: { top: resume.scrollTop, atBottom: resume.atBottom } } : {},
  );
  // Sessions the list has taken away. A deleted session's transcript reports
  // one last position as it unmounts, and that unmount happens after the prune
  // below has already dropped it — so the id is refused outright rather than
  // being written straight back in.
  const goneSessions = useRef<Set<string>>(new Set());
  const recordScroll = useCallback((id: string, top: number, atBottom: boolean) => {
    if (goneSessions.current.has(id)) return;
    scrollPositions.current[id] = { top, atBottom };
  }, []);
  const setDraft = useCallback(
    (id: string, text: string) =>
      setDrafts((d) => (d[id] === text ? d : { ...d, [id]: text })),
    [],
  );
  const patchAttachment = useCallback((sessionId: string, key: string, patch: Partial<Attachment>) => {
    setAttachments((all) => {
      const list = all[sessionId];
      if (!list?.some((a) => a.key === key)) return all;
      return { ...all, [sessionId]: list.map((a) => (a.key === key ? { ...a, ...patch } : a)) };
    });
  }, []);

  // Picked, dropped, or pasted images. Each is uploaded on its own the moment
  // it arrives: the composer stays usable, and a slow picture on a slow
  // connection never blocks typing the question that goes with it.
  // The in-flight upload behind each staged image, so removing one can stop it.
  const uploadsInFlight = useRef<Map<string, AbortController>>(new Map());

  const attachImages = useCallback(
    (files: File[]) => {
      const sessionId = activeId;
      if (!sessionId) return;
      for (const file of files) {
        if (!isSupportedImage(file)) {
          toast.error(`${file.name} is not an image omniplex can send`, {
            description: "PNG, JPEG, GIF and WebP only.",
          });
          continue;
        }
        // Not `crypto.randomUUID`: that exists only in a secure context, and
        // the origins a phone reaches this server on are not one.
        const key = uuid();
        const staged: Attachment = {
          key,
          name: file.name || "pasted image",
          previewUrl: URL.createObjectURL(file),
          status: "uploading",
        };
        setAttachments((all) => ({ ...all, [sessionId]: [...(all[sessionId] ?? []), staged] }));
        // Shrunk before it is sent, not after: a phone camera's 4 MB frame is
        // more picture than any model looks at, and the upload is the slow
        // part of attaching it.
        const abort = new AbortController();
        uploadsInFlight.current.set(key, abort);
        prepareImage(file)
          .then((ready) => {
            if (ready.size > MAX_IMAGE_BYTES) throw new Error("This image is too large to send.");
            return uploadAttachment(sessionId, ready, abort.signal);
          })
          .then((up) => patchAttachment(sessionId, key, { status: "ready", id: up.id }))
          .catch((e: Error) => {
            // An abort means the picture was taken back; there is nothing left
            // to report it to.
            if (e.name !== "AbortError") patchAttachment(sessionId, key, { status: "error", error: e.message });
          })
          .finally(() => uploadsInFlight.current.delete(key));
      }
    },
    [activeId, patchAttachment],
  );

  const removeAttachment = useCallback((sessionId: string, key: string) => {
    uploadsInFlight.current.get(key)?.abort();
    setAttachments((all) => {
      const list = all[sessionId] ?? [];
      const going = list.find((a) => a.key === key);
      if (going) URL.revokeObjectURL(going.previewUrl);
      return { ...all, [sessionId]: list.filter((a) => a.key !== key) };
    });
  }, []);

  const isDesktop = useIsDesktop();
  // Whether the last-session key was set at boot. Read once, before anything
  // can write it, because it decides what the very first frame shows. Storage
  // can be denied outright (Safari with cookies blocked), and a throw here
  // would take the whole mount with it.
  const [hadLastSession] = useState(() => {
    try {
      return localStorage.getItem(LAST_SESSION) !== null;
    } catch {
      return false;
    }
  });
  // True until the first session list lands, which is when we know whether
  // the stored session still exists. Until then a phone must not flash the
  // sidebar open and then shut it again a moment later.
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  const restoreAttempted = useRef(false);
  // Open is the desktop default. On a phone the sidebar *is* the landing
  // screen: with nothing selected there is nothing behind it to look at, so
  // it starts open unless we are about to restore straight into a session.
  const [sidebarOpen, setSidebarOpen] = useState(() => isDesktop || !hadLastSession);
  // Crossing the breakpoint resets it — but only on an actual crossing. On
  // mount this must leave the initial choice above alone.
  const wasDesktop = useRef(isDesktop);
  useEffect(() => {
    if (wasDesktop.current === isDesktop) return;
    wasDesktop.current = isDesktop;
    setSidebarOpen(isDesktop || activeRef.current === null);
  }, [isDesktop]);
  const [creating, setCreating] = useState(false);
  const [projectSettings, setProjectSettings] = useState<Project | "add" | null>(null);
  const [userConfig, setUserConfig] = useState<UserConfig | null>(null);
  // The summary panel. Summaries are held per session so flicking between two
  // sessions does not re-bill a model for an answer we already have; they are
  // deliberately not persisted, because a stale summary read as current is
  // worse than no summary at all.
  const [showSummary, setShowSummary] = useState(false);
  const [summaries, setSummaries] = useState<Record<string, SessionSummary>>({});
  // Progress and failure are per session too, not global: a summary started
  // for one session must not clear the spinner — or show its error — in
  // another one the user has since switched to.
  const [summarizing, setSummarizing] = useState<Record<string, boolean>>({});
  const [summaryErrors, setSummaryErrors] = useState<Record<string, string>>({});
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
  // when the session changed it, the file surface otherwise. An absolute path
  // under the checkout is relativised first; the server only serves the
  // workspace.
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
      onSessions: (list) => {
        setSessions(list);
        setSessionsLoaded(true);
      },
      onHarnesses: (h, cwd) => {
        setHarnesses(h);
        // A harness-only push carries no cwd; keep the one the welcome frame
        // established rather than blanking it.
        if (cwd) setDefaultCwd(cwd);
      },
      onComposerItemsChanged: (id) => {
        if (id === activeRef.current) setComposerRevision((revision) => revision + 1);
      },
      onProjects: setProjects,
      onLabels: setLabels,
      // State only lands for the session currently attached; the client
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

  // Restore the last session once the list arrives. This runs once: after it,
  // "no session selected" is a state the user chose, not one we have yet to
  // resolve, and re-opening the sidebar under them would be wrong.
  useEffect(() => {
    if (!sessionsLoaded || restoreAttempted.current) return;
    restoreAttempted.current = true;
    if (activeId) {
      // Hydrated from the resume cache before the list could say whether the
      // session still exists. It usually does; when it doesn't — deleted or
      // closed from elsewhere while the page was dead — let go the same way
      // a live delete would. The seenActive effect below can't: it only acts
      // on sessions it saw in a list first.
      if (sessions.some((s) => s.id === activeId && s.phase !== "closed")) return;
      // Including the position the cache seeded: the prune below only drops
      // sessions it saw in a list, and this one never made it into one.
      delete scrollPositions.current[activeId];
      goneSessions.current.add(activeId);
      setActiveId(null);
      setState(null);
      setResume(null);
      clientRef.current?.detach();
      if (!isDesktop) setSidebarOpen(true);
      return;
    }
    const last = localStorage.getItem(LAST_SESSION);
    const pick = sessions.find((s) => s.id === last && s.phase !== "closed") ?? null;
    if (pick) select(pick.id);
    // Nothing to restore into, so the phone lands on the sidebar after all.
    else if (!isDesktop) setSidebarOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionsLoaded, sessions]);
  // Until the first list lands we do not know whether there is anything to
  // show, so the content column holds the space rather than announcing "all
  // caught up" to someone with six sessions on a slow connection.
  const restoring = !sessionsLoaded;

  const select = useCallback(
    (id: string) => {
      setActiveId(id);
      activeRef.current = id;
      // The panel belongs to a checkout, so it must not survive a move to a
      // different one.
      setShowChanges(false);
      setChangesExpanded(false);
      // A file asked for in one session means nothing in the next, and another
      // session holding the same path would otherwise open it unasked.
      setPanelRequest(null);
      setState(null);
      localStorage.setItem(LAST_SESSION, id);
      clientRef.current?.attach(id);
      if (!isDesktop) setSidebarOpen(false);
    },
    [isDesktop],
  );

  // The sidebar stays as it was: on a phone the new-session screen covers it
  // completely, so closing it would only mean cancelling drops you onto an
  // empty screen instead of back where you started.
  const startNew = useCallback(() => setCreating(true), []);

  const create = useCallback(
    async (input: NewSessionInput) => {
      const res = await clientRef.current!.command("create_session", input);
      setCreating(false);
      select(res.sessionId);
    },
    [select],
  );

  const listWorkspaces = useCallback(async (projectId: string) => {
    const res = await clientRef.current!.command("list_workspaces", { projectId });
    return (res.workspaces ?? []) as Workspace[];
  }, []);
  // Its own request: `gh` can take seconds, and nothing that shapes a choice
  // should be waiting behind it.
  const listIssues = useCallback(async (projectId: string) => {
    const res = await clientRef.current!.command("list_issues", { projectId });
    return { issues: res.issues ?? [], issuesError: res.issuesError ?? "" };
  }, []);
  const saveUserConfig = useCallback(async (cfg: UserConfig) => { const res=await clientRef.current!.command("save_user_config",{config:cfg}); setUserConfig(res.userConfig); },[]);

  // Summarising starts a harness against a small model, so it can take tens of
  // seconds. The result is keyed by session id: the panel can be closed and
  // reopened, or another session visited and come back to, without paying for
  // the same answer twice.
  const summarize = useCallback(async (id: string) => {
    setSummarizing((prev) => ({ ...prev, [id]: true }));
    setSummaryErrors((prev) => { const { [id]: _gone, ...rest } = prev; return rest; });
    try {
      const res = await clientRef.current!.command("summarize_session", { sessionId: id });
      setSummaries((prev) => ({ ...prev, [id]: res.summary as SessionSummary }));
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      setSummaryErrors((prev) => ({ ...prev, [id]: message }));
    } finally {
      setSummarizing((prev) => { const { [id]: _gone, ...rest } = prev; return rest; });
    }
  }, []);

  // Opening the panel summarises only if there is nothing to show yet. Asking
  // again is a button, not a side effect of looking.
  const openSummary = useCallback(() => {
    if (!activeId) return;
    setShowSummary(true);
    if (!summaries[activeId]) void summarize(activeId);
  }, [activeId, summaries, summarize]);

  // A saved prompt invalidates every summary: they were all written to
  // different instructions and would otherwise sit there looking current.
  const saveSummaryPrompt = useCallback(async (summaryPrompt: string) => {
    // Refusing loudly rather than returning: a silent no-op here would save
    // nothing, re-run against the old prompt, and look like it had worked.
    if (!userConfig) throw new Error("settings are still loading — try again in a moment");
    await saveUserConfig({ ...userConfig, summaryPrompt });
    setSummaries({});
  }, [userConfig, saveUserConfig]);

  // Label mutations fire and forget: the authoritative answer arrives as a
  // labels (or sessions) broadcast, the same way it does for a paired device,
  // so there is no local state to reconcile — only failures to report.
  const setSessionLabel = useCallback((sessionId: string, labelId: string) => {
    clientRef.current?.command("set_session_label", { sessionId, labelId }).catch((e) => {
      toast.error("Could not label that session", { description: e.message });
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

  const addProject = useCallback(async (root: string) => { const res=await clientRef.current!.command("add_project",{root}); setProjects(p=>[res.project,...p.filter(x=>x.id!==res.project.id)]); },[]);
  const saveProject = useCallback(async (projectId:string,config:ProjectConfig) => { const res=await clientRef.current!.command("save_project",{projectId,config}); setProjects(p=>p.map(x=>x.id===projectId?res.project:x)); },[]);
  // Forgetting a project touches nothing on disk, so the only thing to undo
  // locally is the list. The server broadcasts the new one to every other
  // device anyway; dropping it here just means this one does not wait for it.
  const deleteProject = useCallback(async (projectId: string) => {
    await clientRef.current!.command("delete_project", { projectId });
    setProjects((p) => p.filter((x) => x.id !== projectId));
  }, []);

  // Git is the source of truth for what a session changed: it catches the
  // formatter and the codemod as well as the edits we parsed out of tool calls.
  const loadChanges = useCallback(async () => {
    const res = await clientRef.current!.command("session_changes", { sessionId: activeId });
    return res.changes as SessionChanges;
  }, [activeId]);

  const loadFileDiff = useCallback(
    async (path: string) => {
      const res = await clientRef.current!.command("session_file_diff", { sessionId: activeId, path });
      return res.diff as FileDiff;
    },
    [activeId],
  );

  // The real filesystem, for the files and file surfaces: git is the diff
  // surface, and a file the session never touched is exactly what it can't show.
  const loadFileTree = useCallback(
    async (includeIgnored: boolean) => {
      const res = await clientRef.current!.command("session_file_tree", { sessionId: activeId, includeIgnored });
      return res.tree as FileTree;
    },
    [activeId],
  );

  const loadFile = useCallback(
    async (path: string) => {
      const res = await clientRef.current!.command("session_read_file", { sessionId: activeId, path });
      return res.file as FileContent;
    },
    [activeId],
  );

  const send = useCallback(
    (text: string) => {
      if (!activeId) return;
      const staged = attachments[activeId] ?? [];
      const imageIds = staged.filter((a) => a.status === "ready").map((a) => a.id!);
      // Left out entirely when there are none: the overwhelming majority of
      // prompts carry no picture, and the frame is persisted for retry.
      const args = { sessionId: activeId, text, ...(imageIds.length ? { imageIds } : {}) };
      clientRef.current?.command("prompt", args).catch((e) => {
        toast.error("Could not send that prompt", { description: e.message });
      });
      // Cleared optimistically, like the draft: the message is on its way, and
      // the transcript is about to show the same pictures back from the server.
      for (const a of staged) URL.revokeObjectURL(a.previewUrl);
      setAttachments((all) => (all[activeId]?.length ? { ...all, [activeId]: [] } : all));
    },
    [activeId, attachments],
  );

  const loadComposerItems = useCallback(async (): Promise<ComposerItem[]> => {
    if (!activeId) return [];
    const result = await clientRef.current!.command("list_composer_items", { sessionId: activeId });
    return result.items ?? [];
  }, [activeId, composerRevision]);

  const runComposerAction = useCallback(
    async (action: string, args: string, invocation: string) => {
      if (!activeId) return;
      try {
        await clientRef.current!.command("run_composer_action", {
          sessionId: activeId,
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
        toast.info("Session status", { description: parts.join(" · ") });
      }
    },
    [state],
  );

  const cancel = useCallback(() => {
    if (activeId) clientRef.current?.command("cancel", { sessionId: activeId });
  }, [activeId]);

  const resolvePermission = useCallback(
    (requestId: string, outcome: string, optionId: string) => {
      if (activeId) {
        clientRef.current?.command("resolve_permission", {
          sessionId: activeId,
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
          sessionId: activeId,
          requestId,
          action,
          value,
        });
      }
    },
    [activeId],
  );

  // The returned promise settles when the server has *accepted* the delete,
  // not when it is done — the session is gone when it leaves the list, which
  // is what the sidebar waits on. Rejecting it is the sidebar's cue to stop
  // waiting, so the error is re-thrown after it has been reported.
  const remove = useCallback(
    (id: string, removeWorktree: boolean) => {
      if (id !== activeRef.current) select(id);
      const client = clientRef.current;
      if (!client) {
        toast.error("Could not delete that session", { description: "Not connected." });
        return Promise.reject(new Error("not connected"));
      }
      return client.command("delete_session", { sessionId: id, removeWorktree }).catch((e) => {
        toast.error("Could not delete that session", { description: e.message });
        throw e;
      });
    },
    [select],
  );

  const forceDelete = useCallback((id: string) => {
    // Only a worktree omniplex provisioned is omniplex's to destroy, so only that case may
    // promise it. The old copy promised it to every session and kept the
    // promise for one of them.
    const removes = sessions.find((s) => s.id === id)?.workspaceMode === "managed";
    const accepted = window.confirm(
      removes
        ? "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script, removes the recorded Git worktree, and permanently deletes the session."
        : "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script and permanently deletes the session. The checkout is left on disk — omniplex did not create it.",
    );
    if (!accepted) return;
    clientRef.current?.command("force_delete_session", { sessionId: id }).catch((e) => toast.error("Force delete failed", { description: e.message }));
  }, [sessions]);

  // Ask the server to re-probe, for when the user has just installed something.
  const recheck = useCallback(() => {
    clientRef.current?.command("recheck_harnesses", {}).then((res) => {
      if (res?.harnesses) setHarnesses(res.harnesses);
    });
  }, []);

  const meta = useMemo(() => sessions.find((s) => s.id === activeId), [sessions, activeId]);

  // The empty transcript's list of skills to reach for, and the composer it
  // writes into. Both live up here for the same reason the drafts do: the
  // Transcript and the Composer are siblings remounted per session, and this
  // is the one place that can see the catalogue, the project, and the input at
  // once.
  const composerRef = useRef<ComposerHandle>(null);
  const [recents, setRecents] = useState<{ items: ComposerItem[]; seeded: boolean }>({
    items: [],
    seeded: false,
  });
  const projectId = meta?.projectId;
  // Only an empty transcript asks for this, so only an empty transcript pays
  // for the catalogue fetch — and the moment anything arrives in the session
  // the list is dropped rather than lingering behind the conversation.
  const transcriptEmpty = !!state && state.items.length === 0;
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

  // Whether the work in this session has landed, and the confirmation the
  // transcript's prompt opens. The dialog and its guards are the sidebar's
  // own, so "finish with this session" and the row's X are the same action
  // reached from two places; only the sidebar's row animation is not shared,
  // because the transcript has no row.
  const deleteFlow = useDeleteSession({
    sessions,
    onDelete: remove,
    projectRoot: (id) => projects.find((p) => p.id === id)?.root,
  });
  const fetchPR = useCallback(async (sessionId: string): Promise<PullRequest | null> => {
    const res = await clientRef.current!.command("session_pr", { sessionId });
    return (res.pr ?? null) as PullRequest | null;
  }, []);
  // The server checks this too and is the authority; asking here only spares
  // a subprocess for the sessions that plainly have nothing to report.
  const prEligible =
    (meta?.workspaceMode === "managed" || meta?.workspaceMode === "borrowed") && !!meta?.branch;
  const pr = useSessionPR(activeId, prEligible, fetchPR);

  // The permission modes for the attached session's harness. Everything the UI
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
      clientRef.current?.command("set_mode", { sessionId: activeId, mode: modeId }).catch((e) => {
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
      clientRef.current?.command("set_model", { sessionId: activeId, model: modelId }).catch((e) => {
        toast.error("Could not switch model", { description: e.message });
      });
    },
    [activeId],
  );
  const switchEffort = useCallback(
    (effort: string) => {
      if (!activeId) return;
      clientRef.current?.command("set_effort", { sessionId: activeId, effort }).catch((e) => {
        toast.error("Could not change reasoning effort", { description: e.message });
      });
    },
    [activeId],
  );
  const pending = state?.pendingPermissions?.[0];
  const elicitation = state?.pendingElicitations?.[0];
  // The tab is named after whatever is attached, so a phone with several
  // sessions open in several tabs can tell them apart without switching to
  // each one.
  // The list entry, not just the attached state: switching sessions drops
  // `state` until the snapshot lands, and on a slow connection that would
  // leave every tab called "Omniplex" for exactly as long as it takes to
  // reconnect — which is when telling them apart matters most.
  useDocumentTitle(
    activeId
      ? { title: state?.title ?? meta?.title, needsAttention: Boolean(pending || elicitation) }
      : null,
  );
  const activeProject = projects.find((p) => p.id === meta?.projectId);
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

  // The attached session went away (deleted elsewhere, or torn down here).
  //
  // "Absent from the list" only means gone if it was ever in the list: a
  // session we just created is attached before the broadcast carrying it
  // arrives, and treating that gap as a disappearance would detach the
  // session the user is watching being born. So it has to have been seen
  // first. Waiting for `state` instead would be the wrong test — deleting a
  // row that is not the open one selects it first, which clears state, so a
  // delete landing before the first snapshot would leave the app attached to
  // nothing and stuck on "Attaching…" forever.
  //
  // On a phone this also leaves nothing behind the sidebar, so it comes back.
  const seenActive = useRef<string | null>(null);
  useEffect(() => {
    if (!activeId) return;
    if (sessions.some((s) => s.id === activeId)) {
      seenActive.current = activeId;
      return;
    }
    if (seenActive.current !== activeId) return;
    seenActive.current = null;
    setActiveId(null); setState(null); clientRef.current?.detach();
    if (!isDesktop) setSidebarOpen(true);
  }, [sessions, activeId, isDesktop]);

  // Drop drafts for sessions that have left the list, so a deleted session does
  // not leave its text behind for the life of the tab. "Absent from the list"
  // only means gone if the session was ever *in* the list: a freshly created
  // session is attached — and can be typed into — before the broadcast listing
  // it arrives, and treating that gap as a disappearance would prune its draft.
  // Same reasoning, and the same guard, as `seenActive` above.
  const seenSessions = useRef<Set<string>>(new Set());
  useEffect(() => {
    for (const s of sessions) seenSessions.current.add(s.id);
    // The scroll positions go the same way, and for the same reason: a
    // deleted session's offset means nothing, and a new id reusing it would
    // be handed a stranger's place in the transcript.
    for (const s of sessions) goneSessions.current.delete(s.id);
    for (const id of Object.keys(scrollPositions.current)) {
      if (sessions.some((s) => s.id === id) || !seenSessions.current.has(id)) continue;
      delete scrollPositions.current[id];
      goneSessions.current.add(id);
    }
    setDrafts((d) => {
      const live = new Set(sessions.map((s) => s.id));
      const next: Record<string, string> = {};
      let changed = false;
      for (const [id, text] of Object.entries(d)) {
        if (live.has(id) || !seenSessions.current.has(id)) next[id] = text;
        else changed = true;
      }
      return changed ? next : d;
    });
    // Staged images go the same way, releasing their preview URLs as they do:
    // a deleted session must not leak blobs for the life of the tab.
    setAttachments((all) => {
      const live = new Set(sessions.map((s) => s.id));
      const next: Record<string, Attachment[]> = {};
      let changed = false;
      for (const [id, list] of Object.entries(all)) {
        if (live.has(id) || !seenSessions.current.has(id)) next[id] = list;
        else {
          for (const a of list) URL.revokeObjectURL(a.previewUrl);
          changed = true;
        }
      }
      return changed ? next : all;
    });
  }, [sessions]);

  // Nothing measures the composer on its own, so a fixed padding could only
  // ever guess at its height — and it grows (a tall draft, a permission prompt
  // appearing above it) well past any guess. A ResizeObserver on the whole
  // overlay keeps `--composer-h` exactly right, and the transcript reserves
  // `that + headroom` below its tail. Grow the overlay and the content above it
  // visibly rises: it reads as the composer pushing the transcript up, even
  // though it is floating.
  const hasSession = state != null;
  // The resume cache is one boot's worth of help. Its scroll position moved
  // into the per-session map above as the page hydrated, and the transcript
  // takes over reporting from there, so once a session is on screen the blob
  // has done its job.
  useEffect(() => {
    if (resume && hasSession) setResume(null);
  }, [resume, hasSession]);
  useEffect(() => {
    if (!hasSession || typeof ResizeObserver === "undefined") return;
    const overlay = overlayRef.current;
    const layout = chatLayoutRef.current;
    if (!overlay || !layout) return;
    const apply = () =>
      layout.style.setProperty(
        "--composer-h",
        `${Math.ceil(overlay.getBoundingClientRect().height)}px`,
      );
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(overlay);
    return () => {
      ro.disconnect();
      layout.style.removeProperty("--composer-h");
    };
    // themePreview toggles the whole main tree in and out below, so the
    // measured elements are remounted under it: re-run to observe the new ones.
  }, [hasSession, themePreview]);

  if (themePreview) return <Suspense fallback={<div className="flex h-dvh items-center justify-center"><Spinner /></div>}><ThemePreview /></Suspense>;

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar
        sessions={sessions}
        activeId={activeId}
        status={status}
        open={sidebarOpen}
        onOpenChange={setSidebarOpen}
        onSelect={select}
        onNew={startNew}
        onDelete={remove}
        onShowAccess={() => setShowAccess(true)}
        accentOf={accentOf}
        projectName={(id)=>projects.find(p=>p.id===id)?.config.name}
        projectRoot={(id)=>projects.find(p=>p.id===id)?.root}
        labels={labels}
        onSetLabel={setSessionLabel}
        onManageLabels={openLabelManager}
      />

      <DeleteSessionDialog flow={deleteFlow} />

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
            label="Show sessions"
            onClick={() => setSidebarOpen(true)}
            className={cn(sidebarOpen && "hidden")}
          >
            <PanelLeftIcon />
          </IconButton>

          {state ? (
            <>
              <p className="min-w-0 flex-1 truncate text-[13px] font-medium">
                {state.title || "Untitled session"}
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

              {/* Filing the open session — the same menu the sidebar row
                  carries, so a session can be labelled from either place.
                  Invisible until the user has defined a label. */}
              {labels.length > 0 && activeId && (
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
                          aria-label="Label this session"
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
                    onSelect={(labelId) => setSessionLabel(activeId, labelId)}
                    onManage={openLabelManager}
                  />
                </DropdownMenu>
              )}

              <IconButton
                label={showChanges ? "Hide the panel" : "Show the panel"}
                onClick={() => setShowChanges((v) => !v)}
                className={cn("relative", showChanges && "bg-accent")}
              >
                <FileDiffIcon />
                {/* A live agent-count badge: work is happening off-transcript. */}
                {liveAgentCount(state.items) > 0 && (
                  <span className="bg-primary text-primary-foreground absolute -top-0.5 -right-0.5 flex size-3.5 items-center justify-center rounded-full text-[9px] tabular-nums">
                    {liveAgentCount(state.items)}
                  </span>
                )}
              </IconButton>

              <IconButton label="Summarise this session" onClick={openSummary}>
                <SparklesIcon />
              </IconButton>

              <IconButton
                label={transcriptCopied ? "Transcript copied" : "Copy transcript"}
                onClick={() => void copyTranscript(transcriptMarkdown(state.items, state.turns))}
              >
                {transcriptCopied ? <CheckIcon className="text-success" /> : <CopyIcon />}
              </IconButton>

              {activeProject && (
                <IconButton
                  label={`${activeProject.config.name} settings`}
                  onClick={() => setProjectSettings(activeProject)}
                >
                  <SettingsIcon />
                </IconButton>
              )}

            </>
          ) : (
            <span className="text-muted-foreground flex-1 text-[13px]">
              {meta ? "Attaching…" : restoring ? "" : "No session selected"}
            </span>
          )}
        </header>

        {state ? (
          <div ref={chatLayoutRef} className="relative flex min-h-0 flex-1 flex-col">
            {/* Content scrolling up dissolves into the header rather than
                being cut by a border. */}
            <div className="from-background pointer-events-none absolute inset-x-0 top-0 z-10 h-8 bg-gradient-to-b to-transparent" />

            <OpenPathContext.Provider value={openPath}>
              <Transcript key={activeId} state={state} initialScroll={activeId ? scrollPositions.current[activeId] : undefined} onScrollChange={recordScroll} onContinue={()=>activeId&&clientRef.current?.command("continue_session",{sessionId:activeId})} onRetryProvision={()=>activeId&&clientRef.current?.command("retry_provision",{sessionId:activeId})} onCleanup={()=>activeId&&clientRef.current?.command("cleanup_session",{sessionId:activeId})} onForceDelete={()=>activeId&&forceDelete(activeId)} onOpenDiff={openDiff} pr={pr} onFinish={()=>meta&&deleteFlow.ask(meta)} recents={recents.items} recentsSeeded={recents.seeded} onPickRecent={pickRecent} onLoadOlder={()=>clientRef.current?.loadOlder() ?? Promise.resolve()} />
            </OpenPathContext.Provider>

            {/* The mirror of the header fade: content dissolves into the
                composer instead of sliding under a hard edge, and it hides the
                seam where text scrolls past the composer's transparent gutters.
                It sits just above the overlay, tracking its measured height. */}
            <div
              className="from-background pointer-events-none absolute inset-x-0 z-10 h-8 bg-gradient-to-t to-transparent"
              style={{ bottom: "var(--composer-h, 9rem)" }}
            />

            {/* The input floats over the transcript's tail instead of sitting
                in a full-width tray. Anything that blocks the turn — a
                permission or elicitation — stacks above it. */}
            <div ref={overlayRef} className="absolute inset-x-0 bottom-0 z-10">
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

              <Composer
                key={activeId}
                ref={composerRef}
                draft={activeId ? (drafts[activeId] ?? "") : ""}
                onDraftChange={(text) => activeId && setDraft(activeId, text)}
                disabled={state.closed || workspaceCleaning || workspaceFailed}
                sendDisabled={workspacePreparing}
                disabledPlaceholder={workspaceBusy ? (workspaceCleaning ? "Cleaning up workspace…" : "Preparing workspace…") : workspaceFailed ? "Workspace needs attention" : undefined}
                busy={state.phase === "turn"}
                onSend={send}
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
                usage={state.usage}
                loadComposerItems={loadComposerItems}
                onRunClientAction={runClientComposerAction}
                onRunComposerAction={runComposerAction}
                onCommandUsed={noteSkillUsed}
              />
            </div>
          </div>
        ) : (
          <EmptyState
            restoring={restoring}
            hasSessions={sessions.length > 0}
            onNew={startNew}
          />
        )}
      </main>

      {state && activeId && panelLoaded && (
        <Suspense fallback={null}>
          <Panel
          // Remounted per session: the tab model is per-session state.
          key={activeId}
          sessionId={activeId}
          items={state.items}
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
          />
        </Suspense>
      )}

      {showSummary && activeId && (
        <Suspense fallback={null}>
          <SessionSummaryPanel
          summary={summaries[activeId] ?? null}
          loading={!!summarizing[activeId]}
          error={summaryErrors[activeId] ?? null}
          // A summary made before the latest events is still worth reading —
          // it just should not claim to be the whole story.
          stale={!!state && !!summaries[activeId] && summaries[activeId].seq < state.seq}
          userConfig={userConfig}
          onRegenerate={() => void summarize(activeId)}
          onSavePrompt={saveSummaryPrompt}
          onClose={() => setShowSummary(false)}
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

      {creating && (
        <NewSession
          projects={projects}
          harnesses={harnesses}
          userConfig={userConfig}
          onCreate={create}
          onListWorkspaces={listWorkspaces}
          onListIssues={listIssues}
          onAddProject={()=>setProjectSettings("add")}
          onSettings={setProjectSettings}
          onRecheck={recheck}
          status={status}
          onClose={() => setCreating(false)}
        />
      )}
      {projectSettings && (
        <Suspense fallback={null}>
          <ProjectSettings
          project={projectSettings === "add" ? null : projectSettings}
          defaultRoot={defaultCwd}
          harnesses={harnesses}
          userConfig={userConfig}
          onAdd={addProject}
          onSave={saveProject}
          onDelete={deleteProject}
          sessionCount={
            projectSettings === "add"
              ? 0
              : sessions.filter((s) => s.projectId === projectSettings.id).length
          }
          onSaveUserConfig={saveUserConfig}
          onClose={() => setProjectSettings(null)}
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
 * they say different things. A single oversized "New session" button was
 * answering all three with a call to action nobody asked for — on a phone it
 * was the whole landing screen, and on a desktop with sessions in the list it
 * was pointing away from them.
 */
function EmptyState({
  restoring,
  hasSessions,
  onNew,
}: {
  restoring: boolean;
  hasSessions: boolean;
  onNew: () => void;
}) {
  // Mid-restore. Saying anything here would only be contradicted a moment
  // later, so it says nothing and just holds the space.
  if (restoring) {
    return (
      <div className="flex flex-1 items-center justify-center" aria-busy="true">
        <span className="sr-only">Reopening your last session…</span>
        <Spinner className="text-muted-foreground/60 size-5" />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 pb-16 text-center">
      {hasSessions ? (
        <>
          <MessagesSquareIcon aria-hidden className="text-muted-foreground/40 size-7" />
          <div className="max-w-xs">
            <p className="text-[15px] font-medium">Nothing open</p>
            <p className="text-muted-foreground mt-1.5 text-[13px] leading-relaxed">
              Pick a session from the list to jump back into it.
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
        New session
      </Button>
    </div>
  );
}
