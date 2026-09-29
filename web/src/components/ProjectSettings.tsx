import {
  FileIcon,
  FolderGit2Icon,
  FolderIcon,
  FolderOpenIcon,
  FolderPlusIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
import { useEffect, useId, useState } from "react";

import { FolderBrowser, GitHubPicker } from "~/components/FolderSources";
import { Badge } from "~/components/ui/badge";
import { SettingsPane } from "~/components/SettingsPane";
import { Button } from "~/components/ui/button";
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
import { formatEffort } from "~/lib/efforts";
import { cn } from "~/lib/utils";
import type {
  Folder,
  GitHubRepo,
  HarnessMeta,
  Project,
  ProjectDefaults,
} from "~/protocol";

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

/** Where a folder added to a project comes from. Exactly one is set. */
export interface AddFolderRequest {
  path?: string;
  url?: string;
  name?: string;
}

/**
 * The folders a project points at. Adding and removing take effect at once,
 * like removing the project does: they are not settings Save would undo.
 * Removing takes the pointer away and nothing else.
 */
function FoldersSection({
  folders,
  onAdd,
  onRemove,
  listRepos,
}: {
  folders: Folder[];
  onAdd: (req: AddFolderRequest) => Promise<void>;
  onRemove: (folderId: string) => Promise<void>;
  listRepos: () => Promise<GitHubRepo[]>;
}) {
  const [adding, setAdding] = useState<"github" | "folder" | "new" | null>(null);
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setAdding(null);
      setNewName("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const option = (id: "github" | "folder" | "new", label: string, Icon: typeof FolderIcon) => (
    <Button
      variant="outline"
      size="sm"
      aria-pressed={adding === id}
      onClick={() => setAdding(adding === id ? null : id)}
      className={cn("h-11 flex-1 md:h-8", adding === id && "bg-accent")}
    >
      <Icon />
      {label}
    </Button>
  );

  return (
    <div className="space-y-2">
      <SectionHeading>Folders</SectionHeading>
      <ul className="divide-y rounded-lg border">
        {folders.map((f) => (
          <li key={f.id} className="flex min-h-11 items-center gap-2 px-3 py-1.5 md:min-h-9">
            {f.git ? (
              <FolderGit2Icon className="text-muted-foreground size-3.5 shrink-0" />
            ) : (
              <FolderIcon className="text-muted-foreground size-3.5 shrink-0" />
            )}
            <span className="min-w-0 flex-1 font-mono text-[11px] break-all">{f.path}</span>
            {f.git && <Badge variant="secondary">Git</Badge>}
            {folders.length > 1 && (
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Remove ${f.path}`}
                disabled={busy}
                onClick={() => void run(() => onRemove(f.id))}
                className="text-muted-foreground hover:text-destructive size-11 shrink-0 md:size-7"
              >
                <Trash2Icon />
              </Button>
            )}
          </li>
        ))}
      </ul>
      <p className="text-muted-foreground text-[11px]">
        Removing a folder only takes it out of the project. Nothing on disk is touched.
      </p>

      <p className="flex items-center gap-1.5 pt-1 text-[12px]">
        <PlusIcon className="size-3.5" /> Add to project
      </p>
      <div className="flex flex-wrap gap-2">
        {option("github", "From GitHub", FolderGit2Icon)}
        {option("folder", "A folder here", FolderOpenIcon)}
        {option("new", "New folder", FolderPlusIcon)}
      </div>
      {adding === "github" && (
        <GitHubPicker
          listRepos={listRepos}
          busy={busy}
          onChoose={(url) => void run(() => onAdd({ url }))}
        />
      )}
      {adding === "folder" && (
        <FolderBrowser busy={busy} onChoose={(path) => void run(() => onAdd({ path }))} />
      )}
      {adding === "new" && (
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (newName.trim()) void run(() => onAdd({ name: newName.trim() }));
          }}
        >
          <Input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="Folder name"
            aria-label="New folder name"
            autoFocus
            className="min-w-0 flex-1"
          />
          <Button type="submit" disabled={!newName.trim() || busy}>
            {busy && <Spinner aria-hidden className="size-4" />}
            Add
          </Button>
        </form>
      )}
      {error && <p className="text-destructive text-[11px] break-words">{error}</p>}
    </div>
  );
}

/**
 * Forgetting a project. Deliberately not in the footer next to Save: this is
 * the one control on the screen that cannot be undone by editing a field
 * back, and a destructive button sitting a thumb's width from the one you
 * press every time is how it gets pressed by accident on a phone.
 *
 * Nothing on disk goes with it: not its folders, not their worktrees. The copy
 * says so, because "delete" on a screen full of paths reads like it might mean
 * the paths.
 */
function DeleteProjectSection({
  name,
  threadCount,
  onDelete,
  onError,
  busy,
  setBusy,
}: {
  name: string;
  threadCount: number;
  onDelete: () => Promise<void>;
  onError: (message: string | null) => void;
  /** Owned by the screen, not this section: Save has to go dead while a
      delete is in flight. Saving mid-delete writes the project back, and an
      upsert would have resurrected the row the delete had just removed. */
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const [confirming, setConfirming] = useState(false);

  // Threads have transcripts, and often a worktree, behind them. The server
  // refuses this outright; saying so here means the user learns it before
  // pressing rather than from an error afterwards.
  const blocked = threadCount > 0;

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
          ? `${threadCount} thread${threadCount === 1 ? "" : "s"} still belong${threadCount === 1 ? "s" : ""} to this project. Delete ${threadCount === 1 ? "it" : "them"} first.`
          : "Takes it out of Omniplex only. Nothing on disk is touched."}
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
  harnesses,
  onSave,
  onAddFolder,
  onRemoveFolder,
  listRepos,
  onDelete,
  threadCount,
  onDeleted,
  onBack,
}: {
  project: Project;
  harnesses: HarnessMeta[];
  onAddFolder: (projectId: string, req: AddFolderRequest) => Promise<Project>;
  onRemoveFolder: (projectId: string, folderId: string) => Promise<Project>;
  listRepos: () => Promise<GitHubRepo[]>;
  onSave: (id: string, name: string, defaults: ProjectDefaults, folders: Folder[]) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  /** How many threads still belong to this project; a project with any is
      not deletable, and the screen says so before the button is pressed. */
  threadCount: number;
  /** After the project is gone; this screen has nothing left to show. */
  onDeleted: () => void;
  onBack?: () => void;
}) {
  const [name, setName] = useState(project.name);
  const [defs, setDefs] = useState<ProjectDefaults>(project.defaults);
  const [folders, setFolders] = useState<Folder[]>(project.folders);
  const [settingsHarness, setSettingsHarness] = useState(project.defaults.harness ?? "codex");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Any edit makes it saveable again.
  useEffect(() => setSaved(false), [name, defs, folders]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(project.id, name, defs, folders);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const defaults = (patch: Partial<ProjectDefaults>) => setDefs({ ...defs, ...patch });
  const agentDefaults = (
    patch: Partial<NonNullable<ProjectDefaults["harnesses"]>[string]>,
  ) =>
    defaults({
      harnesses: {
        ...defs.harnesses,
        [settingsHarness]: { ...defs.harnesses?.[settingsHarness], ...patch },
      },
    });
  const folder = (id: string, patch: Partial<Folder>) =>
    setFolders(folders.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  // The server's list after an add or remove, keeping edits not saved yet.
  const takeFolders = (next: Project) =>
    setFolders((current) => next.folders.map((f) => current.find((c) => c.id === f.id) ?? f));
  // Copies, base branches and hooks only mean something in a git folder.
  const gitFolders = folders.filter((f) => f.git);

  return (
    <SettingsPane
      title={name || project.name}
      description="Defaults every new thread in this project starts from."
      onBack={onBack}
      error={error}
      footer={
        // Dead while a delete is in flight: a save landing after the
        // delete commits would write the project straight back.
        <Button disabled={busy || deleting || saved} onClick={save}>
          {busy ? "Saving…" : saved ? "Saved" : "Save"}
        </Button>
      }
    >
          {
            <div className="space-y-5">
              <div className="space-y-1.5">
                <Label htmlFor="project-name">Project name</Label>
                <Input
                  id="project-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>

              <Separator />

              <FoldersSection
                folders={folders}
                listRepos={listRepos}
                onAdd={async (req) => takeFolders(await onAddFolder(project.id, req))}
                onRemove={async (id) => takeFolders(await onRemoveFolder(project.id, id))}
              />

              <Separator />

              <div className="space-y-2">
                <SectionHeading>Agent defaults</SectionHeading>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  <Select
                    value={defs.harness ?? ""}
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

                  <Select value={settingsHarness} onValueChange={setSettingsHarness}>
                    <SelectTrigger aria-label="Harness settings" className="w-full">
                      <SelectValue placeholder="Settings for harness" />
                    </SelectTrigger>
                    <SelectContent>
                      {harnesses.map((h) => (
                        <SelectItem key={h.id} value={h.id}>
                          {h.name} settings
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>

                  <Select
                    value={defs.harnesses?.[settingsHarness]?.model || UNSET}
                    onValueChange={(v) => agentDefaults({ model: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default model" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default model</SelectItem>
                      {(() => {
                        const models =
                          harnesses.find((h) => h.id === settingsHarness)?.models ??
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
                        const saved = defs.harnesses?.[settingsHarness]?.model;
                        if (saved && !models.some((m) => m.id === saved)) {
                          items.push(
                            <SelectItem key={saved} value={saved}>
                              {saved}
                            </SelectItem>,
                          );
                        }
                        return items;
                      })()}
                    </SelectContent>
                  </Select>

                  <Select
                    value={defs.harnesses?.[settingsHarness]?.effort || UNSET}
                    onValueChange={(v) => agentDefaults({ effort: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default effort" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default effort</SelectItem>
                      {/* Efforts are per model, so the list is what the default
                          harness's models actually accept. */}
                      {effortsOf(harnesses, settingsHarness).map((e) => (
                        <SelectItem key={e} value={e}>
                          {formatEffort(e)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>

                  {/* Modes belong to the default harness; the list follows it. */}
                  <Select
                    value={defs.harnesses?.[settingsHarness]?.mode || UNSET}
                    onValueChange={(v) => agentDefaults({ mode: v === UNSET ? "" : v })}
                  >
                    <SelectTrigger aria-label="Default permission mode" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNSET}>Default permissions</SelectItem>
                      {(() => {
                        const modes =
                          harnesses.find((h) => h.id === settingsHarness)
                            ?.permissionModes ?? [];
                        const items = modes.map((m) => (
                          <SelectItem key={m.id} value={m.id}>
                            {m.label}
                          </SelectItem>
                        ));
                        // A saved mode the current harness list does not know
                        // still renders, verbatim, rather than vanishing.
                        const saved = defs.harnesses?.[settingsHarness]?.mode;
                        if (saved && !modes.some((m) => m.id === saved)) {
                          items.push(
                            <SelectItem key={saved} value={saved}>
                              {saved}
                            </SelectItem>,
                          );
                        }
                        return items;
                      })()}
                    </SelectContent>
                  </Select>

                  <Select
                    value={defs.workspace ?? "local"}
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
                  The default harness opens first. Switching harness restores this project's model,
                  effort and permissions for that harness.
                </p>
              </div>

              <Separator />

              {gitFolders.map((f) => (
                <div key={f.id} className="space-y-3">
                  <SectionHeading note={gitFolders.length > 1 ? f.path : undefined}>
                    Workspace
                  </SectionHeading>
                  <div className="space-y-1.5">
                    <Label htmlFor={`base-branch-${f.id}`}>Base branch</Label>
                    <Input
                      id={`base-branch-${f.id}`}
                      value={f.baseBranch ?? ""}
                      onChange={(e) => folder(f.id, { baseBranch: e.target.value })}
                      placeholder="main"
                      className="font-mono md:text-[12px]"
                    />
                  </div>
                  <HookField
                    label="Provision"
                    root={f.path}
                    value={f.provision ?? ""}
                    onChange={(v) => folder(f.id, { provision: v })}
                  />
                  <HookField
                    label="Deprovision"
                    root={f.path}
                    value={f.deprovision ?? ""}
                    onChange={(v) => folder(f.id, { deprovision: v })}
                  />
                </div>
              ))}

              {gitFolders.length > 0 && <Separator />}

              <DeleteProjectSection
                name={name}
                threadCount={threadCount}
                busy={deleting}
                setBusy={setDeleting}
                onError={setError}
                onDelete={async () => {
                  await onDelete(project.id);
                  onDeleted();
                }}
              />
            </div>
          }

    </SettingsPane>
  );
}
