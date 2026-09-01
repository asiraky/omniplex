import { FileIcon, FolderIcon, FolderOpenIcon, Trash2Icon } from "lucide-react";
import { useId, useMemo, useState } from "react";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { cloneDestination, parentDirectory } from "~/lib/cloneTarget";
import { cn } from "~/lib/utils";
import type { Issue, Project, ProjectConfig, UserConfig } from "~/protocol";
import { makeFormatter } from "./WorkspacePicker";

// A stand-in issue, so the preview shows a real answer rather than describing one.
const sampleIssue: Issue = {
  number: 482,
  title: "Token refresh 500s after 24h",
  url: "",
  labels: [{ name: "bug" }],
};

/** A section heading, so every group on this screen has the same weight. */
function SectionHeading({ children, note }: { children: React.ReactNode; note?: string }) {
  return (
    <h3 className="text-[12px] font-medium">
      {children}
      {note && <span className="text-muted-foreground font-normal"> · {note}</span>}
    </h3>
  );
}

/**
 * The branch-name function is the operator's own habit, not the project's, so
 * it saves to ~/.omniplex/config.json even though it is edited on this screen.
 */
function BranchFormatField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const id = useId();
  const preview = useMemo(() => {
    const { format, error } = makeFormatter(value);
    if (error) return { text: error, bad: true };
    const out = format(sampleIssue);
    return out
      ? { text: out, bad: false }
      : { text: "function returned nothing for the sample issue", bad: true };
  }, [value]);

  return (
    <div className="space-y-1.5">
      <SectionHeading note="this machine only">Branch names from issues</SectionHeading>
      <p className="text-muted-foreground text-[11px]">
        A JavaScript function, issue in and branch name out. It names the worktrees suggested from
        your open GitHub issues.
      </p>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        rows={4}
        className="scroll-thin font-mono md:text-[11px]"
      />
      <p
        className={cn(
          "font-mono text-[11px] break-all",
          preview.bad ? "text-attention-foreground" : "text-muted-foreground",
        )}
      >
        #{sampleIssue.number} → {preview.text}
      </p>
    </div>
  );
}

interface Listing {
  path: string;
  parent: string;
  dirs: string[];
  files: string[];
}

function HookField({
  label,
  root,
  value,
  onChange,
}: {
  label: string;
  root: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [listing, setListing] = useState<Listing | null>(null);

  const load = async (path: string) => {
    const r = await fetch(
      `/api/fs?path=${encodeURIComponent(path)}&root=${encodeURIComponent(root)}&files=1`,
    );
    if (r.ok) setListing((await r.json()) as Listing);
  };
  const choose = (path: string) => {
    onChange(path.slice(root.replace(/\/$/, "").length + 1));
    setOpen(false);
  };

  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex gap-2">
        <Input
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`scripts/omniplex-${label.toLowerCase()}`}
          className="min-w-0 flex-1 font-mono md:text-[12px]"
        />
        <Button
          variant="outline"
          onClick={() => {
            setOpen(!open);
            if (!open) void load(root);
          }}
        >
          <FolderOpenIcon />
          {open ? "Done" : "Choose…"}
        </Button>
      </div>

      {open && listing && (
        <div className="scroll-thin max-h-44 overflow-y-auto rounded-lg border">
          {listing.path !== root && (
            <button
              type="button"
              onClick={() => void load(listing.parent)}
              className="hover:bg-accent text-muted-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left font-mono text-[12px]"
            >
              <FolderIcon className="size-3.5 shrink-0" />
              ../
            </button>
          )}
          {listing.dirs.map((d) => (
            <button
              key={d}
              type="button"
              onClick={() => void load(`${listing.path}/${d}`)}
              className="hover:bg-accent flex w-full items-center gap-2 px-3 py-1.5 text-left font-mono text-[12px]"
            >
              <FolderIcon className="size-3.5 shrink-0" />
              {d}/
            </button>
          ))}
          {listing.files.map((f) => (
            <button
              key={f}
              type="button"
              onClick={() => choose(`${listing.path}/${f}`)}
              className="hover:bg-accent text-primary flex w-full items-center gap-2 px-3 py-1.5 text-left font-mono text-[12px]"
            >
              <FileIcon className="size-3.5 shrink-0" />
              {f}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/**
 * Forgetting a project. Deliberately not in the footer next to Save: this is
 * the one control on the screen that cannot be undone by editing a field
 * back, and a destructive button sitting a thumb's width from the one you
 * press every time is how it gets pressed by accident on a phone.
 *
 * Nothing on disk goes with it — not the checkout, not its worktrees, not
 * .omniplex/project.json — so re-adding the same directory brings every
 * setting on this screen back. The copy says so, because "delete" on a screen
 * full of paths reads like it might mean the paths.
 */
function DeleteProjectSection({
  name,
  sessionCount,
  onDelete,
  onError,
  busy,
  setBusy,
}: {
  name: string;
  sessionCount: number;
  onDelete: () => Promise<void>;
  onError: (message: string | null) => void;
  /** Owned by the screen, not this section: Save has to go dead while a
      delete is in flight. Saving mid-delete writes the project back, and an
      upsert would have resurrected the row the delete had just removed. */
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const [confirming, setConfirming] = useState(false);

  // Sessions have transcripts, and often a worktree, behind them. The server
  // refuses this outright; saying so here means the user learns it before
  // pressing rather than from an error afterwards.
  const blocked = sessionCount > 0;

  const run = async () => {
    setBusy(true);
    onError(null);
    try {
      await onDelete();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
      setBusy(false);
      setConfirming(false);
    }
  };

  return (
    <div className="space-y-2">
      <SectionHeading note="cannot be undone">Remove project</SectionHeading>
      <p className="text-muted-foreground text-[11px]">
        {blocked
          ? `${sessionCount} session${sessionCount === 1 ? "" : "s"} still belong${sessionCount === 1 ? "s" : ""} to this project. Delete ${sessionCount === 1 ? "it" : "them"} first.`
          : "Takes it out of Omniplex only. The checkout, its worktrees and its project.json are left exactly as they are, so adding the directory again restores these settings."}
      </p>
      {confirming && !blocked ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[12px]">Remove “{name || "Untitled"}”?</span>
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" size="sm" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </Button>
            <Button variant="destructive" size="sm" onClick={() => void run()} disabled={busy}>
              {busy ? (
                <>
                  <Spinner aria-hidden className="size-4" />
                  Removing…
                </>
              ) : (
                "Remove"
              )}
            </Button>
          </div>
        </div>
      ) : (
        <Button
          variant="outline"
          size="sm"
          disabled={blocked}
          onClick={() => setConfirming(true)}
          className="text-destructive hover:text-destructive"
        >
          <Trash2Icon />
          Remove project
        </Button>
      )}
    </div>
  );
}

/** Where a project comes from: a checkout that is already on disk, or one to clone. */
type Source = "existing" | "clone";

export function ProjectSettings({
  project,
  defaultRoot,
  userConfig,
  onAdd,
  onSave,
  onDelete,
  sessionCount,
  onSaveUserConfig,
  onClose,
}: {
  project: Project | null;
  defaultRoot: string;
  userConfig: UserConfig | null;
  /** With a url, the server clones into `root` first — which can take a while. */
  onAdd: (root: string, url?: string) => Promise<void>;
  onSave: (id: string, cfg: ProjectConfig) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  /** How many sessions still belong to this project; a project with any is
      not deletable, and the screen says so before the button is pressed. */
  sessionCount: number;
  onSaveUserConfig: (cfg: UserConfig) => Promise<void>;
  onClose: () => void;
}) {
  const [root, setRoot] = useState(project?.root ?? defaultRoot);
  const [cfg, setCfg] = useState<ProjectConfig>(
    project?.config ?? {
      version: 1,
      name: "",
      defaults: { harness: "codex", harnesses: {}, workspace: "local" },
      workspace: {
        suggestedRoot: ".worktrees",
        provisionTimeoutSeconds: 1800,
        deprovisionTimeoutSeconds: 600,
      },
    },
  );
  const [user, setUser] = useState<UserConfig>(userConfig ?? { version: 1 });
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [source, setSource] = useState<Source>("existing");
  const [url, setUrl] = useState("");
  // The destination tracks the URL until it is edited by hand: typing the repo
  // fills the path in, and touching the path stops it moving under you.
  const [typedDest, setTypedDest] = useState<string | null>(null);

  // Clones go next to the last one. Before there has been one, guess the
  // directory the server's own checkout sits in rather than the checkout
  // itself: the server is started inside a project, and cloning a repository
  // into another repository is never what was meant.
  // The live prop wins over the copy held for editing: this dialog can open
  // before the user config has arrived, and the copy would then be an empty
  // object forever.
  const cloneParent =
    userConfig?.projectsDirectory ||
    user.projectsDirectory ||
    parentDirectory(defaultRoot) ||
    defaultRoot;
  const destination = typedDest ?? cloneDestination(cloneParent, url);
  const cloning = !project && source === "clone";

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      if (project) {
        await onSave(project.id, cfg);
        await onSaveUserConfig(user);
      } else if (cloning) {
        // A real clone over a real network: this is the slow one, and its
        // failure message is the server's own words about why git refused.
        await onAdd(destination.trim(), url.trim());
        // Where it landed is where the next one should be offered. Saved only
        // after the clone worked — a path git rejected is not a habit — and
        // written over the config as it stands on the server rather than the
        // copy this dialog opened with, which may predate it.
        const latest = userConfig ?? user;
        const parent = parentDirectory(destination);
        if (parent && parent !== latest.projectsDirectory) {
          // The project is already registered. Failing the dialog now would
          // ask the operator to repeat a clone that worked, and the repeat
          // would be refused for a destination that now exists.
          try {
            await onSaveUserConfig({ ...latest, projectsDirectory: parent });
          } catch {
            // Forgetting where the last clone went costs the next prefill.
          }
        }
      } else await onAdd(root);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const defaults = (patch: Partial<ProjectConfig["defaults"]>) =>
    setCfg({ ...cfg, defaults: { ...cfg.defaults, ...patch } });
  const workspace = (patch: Partial<ProjectConfig["workspace"]>) =>
    setCfg({ ...cfg, workspace: { ...cfg.workspace, ...patch } });

  // Nothing to add until the form names something to add: a directory, or a
  // repository and somewhere to put it.
  const addable = cloning ? !!url.trim() && !!destination.trim() : !!root;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      {/* Header and footer stay put; only the form between them scrolls. */}
      <DialogContent
        fullscreenOnMobile
        className="flex max-h-[min(90dvh,44rem)] flex-col gap-0 p-0 md:max-w-lg"
      >
        <DialogHeader className="border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>{project ? `${cfg.name} settings` : "Add project"}</DialogTitle>
          <DialogDescription>
            {project
              ? "How this project's workspaces are made, and what to do with it."
              : "Point Omniplex at a Git checkout, or clone one, to start creating sessions in it."}
          </DialogDescription>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
          {!project ? (
            <div className="space-y-4">
              {/* Two ways in, both first-class. Full-width rows rather than a
                  segmented control: at 375px a two-up toggle with these
                  labels wraps into something you cannot aim at. */}
              <div role="radiogroup" aria-label="Project source" className="grid gap-2 sm:grid-cols-2">
                {(
                  [
                    ["existing", "Existing folder", "A checkout already on this machine."],
                    ["clone", "Clone from Git", "Omniplex clones it, then adds it."],
                  ] as [Source, string, string][]
                ).map(([id, label, hint]) => (
                  <button
                    key={id}
                    type="button"
                    role="radio"
                    aria-checked={source === id}
                    onClick={() => setSource(id)}
                    className={cn(
                      "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2",
                      source === id ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
                    )}
                  >
                    <span className="text-[13px] leading-tight">{label}</span>
                    <span className="text-muted-foreground text-[11px] leading-tight">{hint}</span>
                  </button>
                ))}
              </div>

              {source === "existing" ? (
                <div className="space-y-1.5">
                  <Label htmlFor="project-root">Project directory</Label>
                  <Input
                    id="project-root"
                    value={root}
                    onChange={(e) => setRoot(e.target.value)}
                    className="font-mono md:text-[12px]"
                  />
                </div>
              ) : (
                <>
                  <div className="space-y-1.5">
                    <Label htmlFor="project-url">Repository</Label>
                    <Input
                      id="project-url"
                      value={url}
                      onChange={(e) => setUrl(e.target.value)}
                      placeholder="asiraky/omniplex"
                      autoCapitalize="off"
                      autoCorrect="off"
                      spellCheck={false}
                      className="font-mono md:text-[12px]"
                    />
                    <p className="text-muted-foreground text-[11px]">
                      A clone URL, or just owner/repo.
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="project-destination">Clone into</Label>
                    <Input
                      id="project-destination"
                      value={destination}
                      onChange={(e) => setTypedDest(e.target.value)}
                      placeholder={`${cloneParent}/repo`}
                      autoCapitalize="off"
                      autoCorrect="off"
                      spellCheck={false}
                      className="font-mono md:text-[12px]"
                    />
                  </div>
                </>
              )}
            </div>
          ) : (
            <div className="space-y-5">
              <div className="space-y-1.5">
                <Label htmlFor="project-name">Project name</Label>
                <Input
                  id="project-name"
                  value={cfg.name}
                  onChange={(e) => setCfg({ ...cfg, name: e.target.value })}
                />
                <p className="text-muted-foreground font-mono text-[10px] break-all">
                  {project.root}/.omniplex/project.json
                </p>
              </div>

              <Separator />

              <div className="space-y-3">
                <SectionHeading>Workspace</SectionHeading>
                <div className="space-y-1.5">
                  <Label htmlFor="default-workspace">Default workspace</Label>
                  <Select
                    value={cfg.defaults.workspace ?? "local"}
                    onValueChange={(v) => defaults({ workspace: v })}
                  >
                    <SelectTrigger id="default-workspace" aria-label="Default workspace" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">Main checkout</SelectItem>
                      <SelectItem value="managed">Worktree</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="text-muted-foreground text-[11px]">
                    Which checkout a new session opens on before you change it. The harness, model,
                    effort and permissions are not set here: the new-session dialog opens on
                    whatever you last started this project with, per harness.
                  </p>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="base-branch">Base branch</Label>
                  <Input
                    id="base-branch"
                    value={cfg.defaults.baseBranch ?? ""}
                    onChange={(e) => defaults({ baseBranch: e.target.value })}
                    placeholder="main"
                    className="font-mono md:text-[12px]"
                  />
                </div>
                <HookField
                  label="Provision"
                  root={project.root}
                  value={cfg.workspace.provision ?? ""}
                  onChange={(v) => workspace({ provision: v })}
                />
                <HookField
                  label="Deprovision"
                  root={project.root}
                  value={cfg.workspace.deprovision ?? ""}
                  onChange={(v) => workspace({ deprovision: v })}
                />
              </div>

              <Separator />

              <BranchFormatField
                value={user.branchFormat ?? ""}
                onChange={(v) => setUser({ ...user, branchFormat: v })}
              />

              <Separator />

              <DeleteProjectSection
                name={cfg.name}
                sessionCount={sessionCount}
                busy={deleting}
                setBusy={setDeleting}
                onError={setError}
                onDelete={async () => {
                  await onDelete(project.id);
                  onClose();
                }}
              />
            </div>
          )}

          {error && (
            <Alert variant="destructive">
              <AlertDescription className="font-mono text-[11px] break-words">
                {error}
              </AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter className="border-t px-6 py-4 pb-[calc(1rem+env(safe-area-inset-bottom))] md:pb-4">
          <Button variant="ghost" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          {/* Dead while a delete is in flight: a save landing after the
              delete commits would write the project straight back. */}
          {/* A clone is a network operation on someone else's server: it can
              run for minutes, so it says what it is doing rather than looking
              like a save that hung. */}
          <Button disabled={busy || deleting || (!project && !addable)} onClick={save}>
            {busy ? (
              <>
                {cloning && <Spinner aria-hidden className="size-4" />}
                {project ? "Saving…" : cloning ? "Cloning…" : "Adding…"}
              </>
            ) : project ? (
              "Save"
            ) : cloning ? (
              "Clone and add"
            ) : (
              "Add project"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
