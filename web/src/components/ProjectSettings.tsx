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
import { formatEffort } from "~/lib/efforts";
import { cn } from "~/lib/utils";
import type { HarnessMeta, Issue, Project, ProjectConfig, UserConfig } from "~/protocol";
import { makeFormatter } from "./WorkspacePicker";

// A stand-in issue, so the preview shows a real answer rather than describing one.
const sampleIssue: Issue = {
  number: 482,
  title: "Token refresh 500s after 24h",
  url: "",
  labels: [{ name: "bug" }],
};

/**
 * The effort levels to offer when no model says. Harnesses report their own —
 * and they differ, Codex's newest models adding "ultra" — so this is only the
 * floor for a harness that has not been asked yet.
 */
const FALLBACK_EFFORTS = ["low", "medium", "high", "xhigh", "max"];

/**
 * Radix rejects "" as a value, so "no preference" needs a sentinel — and it
 * cannot be a plausible id. "default" was one: Claude ships a model *and* a
 * permission mode called exactly that, so choosing either silently saved "no
 * preference" instead.
 */
const UNSET = "__omniplex_unset__";

/**
 * Every effort level the harness's models accept, in the order they report
 * them. A fixed low…max list would drop levels a harness has and offer ones it
 * has not — Codex advertises "ultra" on its newest models only.
 */
function effortsOf(harnesses: HarnessMeta[], harnessId: string): string[] {
  const models = harnesses.find((h) => h.id === harnessId)?.models ?? [];
  const seen: string[] = [];
  for (const model of models) {
    for (const effort of model.efforts ?? []) {
      if (!seen.includes(effort)) seen.push(effort);
    }
  }
  return seen.length > 0 ? seen : FALLBACK_EFFORTS;
}

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
}: {
  name: string;
  sessionCount: number;
  onDelete: () => Promise<void>;
  onError: (message: string | null) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

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

export function ProjectSettings({
  project,
  defaultRoot,
  harnesses,
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
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
  onAdd: (root: string) => Promise<void>;
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
      defaults: { harness: "codex", workspace: "local" },
      workspace: {
        suggestedRoot: ".worktrees",
        provisionTimeoutSeconds: 1800,
        deprovisionTimeoutSeconds: 600,
      },
    },
  );
  const [user, setUser] = useState<UserConfig>(userConfig ?? { version: 1 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      if (project) {
        await onSave(project.id, cfg);
        await onSaveUserConfig(user);
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
              ? "Defaults every new session in this project starts from."
              : "Point Omniplex at a Git checkout to start creating sessions in it."}
          </DialogDescription>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
          {!project ? (
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

              <div className="space-y-2">
                <SectionHeading>Agent defaults</SectionHeading>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  <Select
                    value={cfg.defaults.harness ?? ""}
                    onValueChange={(v) => defaults({ harness: v })}
                  >
                    <SelectTrigger aria-label="Default harness" className="w-full">
                      <SelectValue placeholder="Harness" />
                    </SelectTrigger>
                    <SelectContent>
                      {harnesses.map((h) => (
                        <SelectItem key={h.id} value={h.id}>
                          {h.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>

                  <Select
                    value={cfg.defaults.model || UNSET}
                    onValueChange={(v) => defaults({ model: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default model" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default model</SelectItem>
                      {(() => {
                        const models =
                          harnesses.find((h) => h.id === (cfg.defaults.harness ?? ""))?.models ??
                          [];
                        const items = models.map((m) => (
                          <SelectItem key={m.id} value={m.id}>
                            {m.label}
                            {m.version && (
                              <span className="text-muted-foreground text-[11px]">{m.version}</span>
                            )}
                          </SelectItem>
                        ));
                        // A saved model the current harness list does not know
                        // still renders, verbatim, rather than vanishing.
                        if (cfg.defaults.model && !models.some((m) => m.id === cfg.defaults.model)) {
                          items.push(
                            <SelectItem key={cfg.defaults.model} value={cfg.defaults.model}>
                              {cfg.defaults.model}
                            </SelectItem>,
                          );
                        }
                        return items;
                      })()}
                    </SelectContent>
                  </Select>

                  <Select
                    value={cfg.defaults.effort || UNSET}
                    onValueChange={(v) => defaults({ effort: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default effort" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default effort</SelectItem>
                      {/* Efforts are per model, so the list is what the default
                          harness's models actually accept. */}
                      {effortsOf(harnesses, cfg.defaults.harness ?? "").map((e) => (
                        <SelectItem key={e} value={e}>
                          {formatEffort(e)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>

                  {/* Modes belong to the default harness; the list follows it. */}
                  <Select
                    value={cfg.defaults.mode || UNSET}
                    onValueChange={(v) => defaults({ mode: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default permission mode" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default permissions</SelectItem>
                      {(() => {
                        const modes =
                          harnesses.find((h) => h.id === (cfg.defaults.harness ?? ""))
                            ?.permissionModes ?? [];
                        const items = modes.map((m) => (
                          <SelectItem key={m.id} value={m.id}>
                            {m.label}
                          </SelectItem>
                        ));
                        // A saved mode the current harness list does not know
                        // still renders, verbatim, rather than vanishing.
                        if (cfg.defaults.mode && !modes.some((m) => m.id === cfg.defaults.mode)) {
                          items.push(
                            <SelectItem key={cfg.defaults.mode} value={cfg.defaults.mode}>
                              {cfg.defaults.mode}
                            </SelectItem>,
                          );
                        }
                        return items;
                      })()}
                    </SelectContent>
                  </Select>

                  <Select
                    value={cfg.defaults.workspace ?? "local"}
                    onValueChange={(v) => defaults({ workspace: v })}
                  >
                    <SelectTrigger aria-label="Default workspace" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">Main checkout</SelectItem>
                      <SelectItem value="managed">Worktree</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <p className="text-muted-foreground text-[11px]">
                  Defaults only preselect the new-session dialog. Every session can still be started
                  any other way.
                </p>
              </div>

              <Separator />

              <div className="space-y-3">
                <SectionHeading>Workspace</SectionHeading>
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
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || (!project && !root)} onClick={save}>
            {busy ? "Saving…" : project ? "Save" : "Add project"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
