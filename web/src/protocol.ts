// Wire types for the sync protocol. These mirror the Go definitions in
// internal/proto and internal/server; the protocol is the contract between
// them, and no language owns it.

export type StopReason =
  | "end_turn"
  | "max_tokens"
  | "refusal"
  | "cancelled"
  | "error";

export type ToolStatus = "pending" | "in_progress" | "completed" | "failed";

export type ToolKind =
  | "read"
  | "edit"
  | "delete"
  | "move"
  | "search"
  | "execute"
  | "think"
  | "fetch"
  | "other";

export type PermissionOutcome =
  | "allow_once"
  | "allow_always"
  | "reject_once"
  | "reject_always"
  | "cancelled";

export interface ToolContent {
  type: "text" | "diff";
  text?: string;
  path?: string;
  oldText?: string;
  newText?: string;
}

/** One image a human attached to a prompt. The bytes are read back from the
    attachment endpoint, so a transcript replayed on another device shows the
    picture without it ever having been in the event log. */
export interface PromptImage {
  id: string;
  mediaType: string;
}

export interface Item {
  id: string;
  kind: "message" | "tool" | "notice";
  turnId?: string;
  /** The Task/Agent tool call this item's work happened inside, for subagents. */
  parentId?: string;
  /** When the item's first event landed, in millis. Display metadata only. */
  receivedAt?: number;
  role?: "user" | "agent";
  contentKind?: "text" | "thought";
  text?: string;
  /** Images on a user message. */
  images?: PromptImage[];
  toolKind?: ToolKind;
  title?: string;
  status?: ToolStatus;
  input?: unknown;
  content?: ToolContent[];
  // notice
  noticeKind?: "compaction";
  /** For a compaction notice: whether the harness ("auto") or a human triggered it. */
  trigger?: string;
  preTokens?: number;
  postTokens?: number;
}

/** One file the session changed, aggregated across the whole session. */
export interface ChangedFile {
  path: string;
  /** The name the file had at the base, for a rename. */
  oldPath?: string;
  status: "added" | "modified" | "deleted" | "renamed" | "copied";
  additions: number;
  deletions: number;
  binary?: boolean;
  untracked?: boolean;
}

/** The PR-style file list for a session's checkout, measured by Git itself. */
export interface SessionChanges {
  root: string;
  branch?: string;
  baseRef?: string;
  base?: string;
  files: ChangedFile[];
  additions: number;
  deletions: number;
  truncated?: boolean;
  /** Why the list is empty, when the reason is not "nothing changed". */
  warning?: string;
}

/** Every path under a session's checkout, relative to its root. */
export interface FileTree {
  root: string;
  files: string[];
  truncated?: boolean;
  /** Why the list is empty, when the reason is not "an empty checkout". */
  warning?: string;
}

/** One file's bytes, for the read-only viewer. */
export interface FileContent {
  path: string;
  content: string;
  size: number;
  binary?: boolean;
  truncated?: boolean;
}

export interface FileDiff {
  path: string;
  oldPath?: string;
  status: string;
  patch: string;
  binary?: boolean;
  truncated?: boolean;
}

/** One provider-native composer completion, normalized by the live adapter. */
export interface ComposerItem {
  id: string;
  name: string;
  description?: string;
  kind: "command" | "skill";
  trigger: string;
  insertText: string;
  argsHint?: string;
  origin?: "personal" | "repo" | "project" | "plugin" | "system" | "built-in" | "other";
  behavior: "prompt" | "client-action" | "adapter-action";
  action?: string;
  aliases?: string[];
}

/**
 * What one turn changed on disk, measured between the snapshots bracketing it.
 * A turn that changed nothing has none: the server does not report an empty
 * list, because "0 files changed" is noise on every turn that was a question.
 */
export interface TurnDiff {
  turnId: string;
  files: ChangedFile[];
  additions: number;
  deletions: number;
  truncated?: boolean;
  /** Why the turn's changes could not be measured, when they could not be. */
  error?: string;
}

export interface TurnRecovery {
  resumeOf: string;
  attempt: number;
  /** Why the turn being continued stopped. "restart" is the server dying
      under it; "error" is the turn failing on its own — an expired login is
      the usual one — with nothing having restarted. Absent on turns recorded
      before the field existed; read those as "restart". */
  cause?: "restart" | "error";
}

/** The error a turn is closed with when a resume finds it still open, matched
    exactly (proto.ErrServerRestarted in Go). Anything else in `Turn.error` is
    the harness's own explanation and belongs on screen verbatim. */
export const SERVER_RESTARTED = "server restarted during turn";

export interface Turn {
  id: string;
  prompt: string;
  images?: PromptImage[];
  stopReason?: StopReason;
  error?: string;
  done: boolean;
  // Present only on a turn that continues an earlier one: after a restart the
  // server starts it itself, after an error someone asks for it.
  recovery?: TurnRecovery;
  // What the turn changed on disk. Absent until the turn has finished and been
  // measured, and on any turn that changed nothing.
  diff?: TurnDiff;
  // When the turn started and finished, from the event log's own clock
  // (epoch milliseconds). The fold over a finished turn is labelled
  // "Worked for 34s" from these.
  startedAt?: number;
  finishedAt?: number;
}

export interface PermissionOption {
  optionId: string;
  name: string;
  kind: PermissionOutcome;
}

export interface PendingPermission {
  requestId: string;
  toolCallId: string;
  toolName: string;
  title: string;
  input?: unknown;
  options: PermissionOption[];
}

export interface PendingElicitation {
  requestId: string;
  prompt: string;
  schema: {
    type?: string;
    properties?: Record<string, Record<string, any>>;
    required?: string[];
    "x-url"?: string;
  };
}

export interface Usage {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cost: number;
  /** How full the context window is, 0–100+ (unclamped). Absent when the harness cannot say. */
  contextPct?: number;
  /** Tokens currently in the context window. */
  contextUsed?: number;
  /** The window occupancy is measured against — the auto-compaction window. */
  contextWindow?: number;
  /** The model's full context window, when it is larger than contextWindow
   *  (i.e. auto-compaction runs against a tighter boundary). */
  contextLimit?: number;
  /** Whether the harness compacts automatically as the window fills. When
   *  false, the window is a hard limit and no compaction will happen. */
  autoCompact?: boolean;
  /** The token count at which auto-compaction triggers, when reported. */
  autoCompactThreshold?: number;
  /** Per-category breakdown of what occupies the window, for a segmented bar. */
  contextCategories?: { name: string; tokens: number }[];
}

export interface PlanEntry {
  content: string;
  status: string;
  priority?: string;
}

export interface SessionState {
  sessionId: string;
  seq: number;
  cwd: string;
  harness: string;
  model: string;
  mode: string;
  effort: string;
  title: string;
  phase: "creating" | "provisioning" | "provision_failed" | "idle" | "turn" | "cleaning" | "cleanup_failed" | "closed";
  closed: boolean;
  workspace: WorkspaceState;
  items: Item[];
  turns: Turn[];
  plan: PlanEntry[];
  usage: Usage;
  pendingPermissions: PendingPermission[];
  pendingElicitations: PendingElicitation[];
}

export interface WorkspaceState {
  phase: string;
  projectId?: string;
  projectRoot?: string;
  mode?: string;
  effort?: string;
  branch?: string;
  baseRef?: string;
  hook?: string;
  command?: string;
  output?: string;
  error?: string;
  exitCode?: number;
  startedAt?: number;
  durationMs?: number;
  resources?: Record<string, unknown>;
  deleteAfterCleanup?: boolean;
}

export interface ProjectConfig {
  version: number;
  name: string;
  defaults: { harness?: string; model?: string; effort?: string; mode?: string; workspace?: string; baseBranch?: string };
  workspace: { suggestedRoot?: string; provision?: string; deprovision?: string; provisionTimeoutSeconds?: number; deprovisionTimeoutSeconds?: number };
}

export interface Project { id: string; root: string; config: ProjectConfig; createdAt: number; updatedAt: number }

/** One checkout a session could run in: the project root, or any worktree Git knows about. */
export interface Workspace {
  path: string;
  branch?: string;
  /** Short commit, for a detached worktree that has no branch. */
  head?: string;
  isRoot?: boolean;
  /** A live session already holds this checkout. Selectable anyway; the picker warns. */
  busy?: boolean;
  busySessionId?: string;
  busyTitle?: string;
  locked?: boolean;
}

/** A `gh issue list` row, passed through verbatim so the user's own format function decides what matters. */
export interface Issue {
  number: number;
  title: string;
  url?: string;
  labels?: { name: string }[];
  assignees?: unknown;
}

/**
 * The pull request for a session's branch, as `gh pr view` reports it. Fetched
 * on demand and never stored: it stops being true the moment someone merges.
 */
export interface PullRequest {
  number: number;
  title?: string;
  url?: string;
  /** gh's own word: OPEN, MERGED or CLOSED. */
  state?: string;
  /** Both a merged state and a merge timestamp; neither alone is trusted. */
  merged?: boolean;
  mergedAt?: string;
  /** The commit the branch pointed at when it merged; a branch that has moved
   *  past it has unmerged work, and is not reported as merged. */
  headRefOid?: string;
}

/**
 * Per-machine preferences, kept in ~/.omniplex/config.json rather than the repo:
 * branchFormat is the operator's own naming habit, not the project's.
 */
export interface UserConfig {
  version: number;
  /** A JavaScript arrow function, issue in and branch name out, evaluated here. */
  branchFormat?: string;
  suggestIssues?: boolean;
  /**
   * The system prompt the session summariser runs under. Empty means the
   * server's default, so clearing the box is how you go back to it.
   */
  summaryPrompt?: string;
}

/**
 * One generated session summary. `seq` is the session head it was made from,
 * so a client can tell a summary that still describes the session from one the
 * session has since moved past.
 */
export interface SessionSummary {
  text: string;
  harness: string;
  model: string;
  seq: number;
  generatedAt: number;
}

export interface SessionMeta {
  id: string;
  cwd: string;
  harness: string;
  /**
   * The provider instance (account) the session was created under. Absent on
   * sessions from before instances existed; those resolve to the default
   * instance of their harness.
   */
  providerInstance?: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  headSeq: number;
  phase: string;
  /**
   * The derived whose-turn-is-it signal, filled by the server from the live
   * projection: working | needs_permission | needs_answer | needs_prompt |
   * failed | closed. This — not phase — is what the sidebar indicators and
   * anything routing on session state should read.
   */
  attention?: string;
  projectId?: string;
  branch?: string;
  model?: string;
  mode?: string;
  workspaceMode?: string;
  /**
   * The user-defined label this session is filed under, or absent for
   * unlabelled. One label per session — a status, not a tag set.
   */
  labelId?: string;
}

/**
 * One user-defined label. User-level, not per-project: the same set applies
 * everywhere, and the server re-broadcasts the whole list on any change so
 * every paired device stays current. A label means only what the user decides
 * it means — nothing in the system reads it back.
 */
export interface Label {
  id: string;
  name: string;
  color: string;
  /** The user's chosen sidebar order, smallest first. */
  position: number;
  /**
   * Start this group folded. Only the default is shared; live collapse state
   * is device-local, like the sidebar width.
   */
  collapsedByDefault?: boolean;
  createdAt: number;
}

/**
 * One selectable model, exactly as the harness describes itself. Everything
 * but `group` is the harness's own answer — omniplex asks it what it offers rather
 * than shipping a list that goes stale — and `group` is the adapter's single
 * presentation call, since no harness reports what it considers superseded.
 */
export interface ModelMeta {
  id: string;
  label: string;
  /** The generation behind the label: "Opus 5 with 1M context", "5.6". */
  version?: string;
  /** The harness's one-line summary of what the model is for. */
  description?: string;
  /** What an alias actually runs, so "Default" can name a real model. */
  resolves?: string;
  /** "legacy" for a superseded model a picker should fold away. */
  group?: string;
  /** The model the harness itself would pick. A picker preselects it. */
  default?: boolean;
  /** The reasoning levels this model accepts, most modest first. */
  efforts?: string[];
}

/** The group id for models kept for continuity rather than offered first. */
export const LEGACY_GROUP = "legacy";

/**
 * One permission preset a harness offers. The id is opaque here: only the
 * adapter that declared it knows what it maps onto (Claude's single enum,
 * Codex's approval-policy × sandbox pair, or whatever a future harness has).
 */
export interface PermissionModeMeta {
  id: string;
  label: string;
  description?: string;
  /** Selected when the user has expressed no preference. */
  default?: boolean;
}

export interface Remedy {
  text: string;
  url?: string;
  command?: string;
}

export interface Availability {
  state: "ready" | "unavailable";
  reason?: string;
  remedy?: Remedy[];
  facts?: Record<string, string>;
}

/**
 * Everything the UI knows about a harness comes from the server, which gets it
 * from the adapter. Nothing here is hardcoded per harness, so adding one needs
 * no client change.
 */
/**
 * One configured account for a harness. The id is the routing key — sessions
 * and create commands name instances, never drivers — while driver selects the
 * logo and accent, so two Codex accounts look like the same product under
 * different names. Availability and models are per instance: one account being
 * logged out must not mark the other unavailable.
 */
export interface ProviderInstanceMeta {
  id: string;
  driver: string;
  displayName: string;
  enabled: boolean;
  availability: Availability;
  models?: ModelMeta[];
}

export interface HarnessMeta {
  id: string;
  name: string;
  accent: string;
  docsUrl?: string;
  models: ModelMeta[];
  permissionModes: PermissionModeMeta[];
  availability: Availability;
  /**
   * Every account configured for this harness, default first. With a single
   * instance this is one entry whose id equals the harness id, which is why
   * the one-account case renders exactly as before.
   */
  instances: ProviderInstanceMeta[];
}

export type Reachability = "loopback" | "lan" | "overlay" | "public";

export interface Endpoint {
  id: string;
  label: string;
  url: string;
  reachability: Reachability;
  /** True for an address that survives a DHCP lease or a change of network. */
  stable?: boolean;
  /**
   * True when the transport protects the traffic: TLS, or WireGuard for an
   * overlay address. A plain LAN address is not, and anything crossing it —
   * the pairing code, then the device token — is readable by whoever controls
   * that network.
   */
  encrypted?: boolean;
}

export interface OverlayInfo {
  installed: boolean;
  running: boolean;
  dnsName?: string;
  https: boolean;
  httpsUrl?: string;
}

export interface Access {
  endpoints: Endpoint[];
  overlay: OverlayInfo;
}

export interface Event {
  sessionId: string;
  seq: number;
  timestamp: number;
  type: string;
  payload: any;
}

export interface ServerFrame {
  type:
    | "welcome"
    | "sessions"
    | "harnesses"
    | "labels"
    | "composer_items_changed"
    | "snapshot"
    | "event"
    | "synchronized"
    | "resync"
    | "ack"
    | "error"
    | "pong";
  serverId?: string;
  /** Content hash of the server's UI bundle; a mismatch means we are stale. */
  build?: string;
  sessions?: SessionMeta[];
  harnesses?: HarnessMeta[];
  projects?: Project[];
  /** Absent means none defined: an empty list is omitted from the frame. */
  labels?: Label[];
  cwd?: string;
  access?: Access;
  sessionId?: string;
  seq?: number;
  state?: SessionState;
  event?: Event;
  commandId?: string;
  result?: any;
  error?: string;
}
