import {
  ArrowLeftIcon,
  CopyIcon,
  CornerDownLeftIcon,
  EllipsisVerticalIcon,
  FileIcon,
  PencilIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Markdown } from "~/components/Markdown";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Spinner } from "~/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { useCopy } from "~/lib/clipboard";
import { fileIconFor } from "~/lib/fileIcons";
import {
  errorText,
  fmtDate,
  fmtSize,
  harnessLabel,
  joinWords,
  normalizeSkill,
  splitFrontmatter,
  type Skill,
  type SkillDetail,
  type SkillFileContent,
  type Source,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { InvocationSection } from "./InvocationSection";
import { ErrorLine, OriginBadge, ProblemText, SectionHeading, Segmented, type SkillsCommand } from "./parts";

type ViewMode = "preview" | "source";

const isMarkdown = (path: string) => /\.(md|markdown|mdx)$/i.test(path);

function stripDetail(d: SkillDetail): Skill {
  const { content: _content, files: _files, ...skill } = d;
  return skill;
}

/** SKILL.md's frontmatter as a compact key/value block above the body. */
function FrontmatterBlock({ fields }: { fields: [string, string][] }) {
  if (fields.length === 0) return null;
  return (
    <dl className="bg-muted/40 mb-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-lg border px-3 py-2">
      {fields.map(([key, value], i) => (
        <div key={`${key}-${i}`} className="contents">
          <dt className="text-muted-foreground font-mono text-[10.5px] leading-5">{key}</dt>
          <dd className="min-w-0 text-[12px] leading-5 break-words">{value || <span className="text-muted-foreground">…</span>}</dd>
        </div>
      ))}
    </dl>
  );
}

function SourceView({ text }: { text: string }) {
  return (
    <pre className="bg-muted/30 overflow-x-auto rounded-lg border px-3 py-2 font-mono text-[11.5px] leading-relaxed break-words whitespace-pre-wrap">
      {text}
    </pre>
  );
}

function DocumentView({ text, path, mode }: { text: string; path: string; mode: ViewMode }) {
  if (mode === "source" || !isMarkdown(path)) return <SourceView text={text} />;
  const { fields, body } = splitFrontmatter(text);
  return (
    <>
      <FrontmatterBlock fields={fields} />
      <Markdown text={body} className="text-[13px] leading-relaxed break-words" />
    </>
  );
}

/** Where an installed skill came from, in a sentence or two. */
function SourceLine({ source }: { source: Source }) {
  const installed = fmtDate(source.installedAt);
  const updated = fmtDate(source.updatedAt);
  return (
    <section aria-label="Source" className="mt-5">
      <SectionHeading>Source</SectionHeading>
      <p className="px-1 text-[12.5px] leading-snug break-words">
        From <span className="font-mono text-[12px]">{source.repo}</span>
        {source.ref && (
          <>
            {" "}
            at <span className="font-mono text-[12px]">{source.ref}</span>
          </>
        )}
        {source.path && (
          <>
            , folder <span className="font-mono text-[12px]">{source.path}</span>
          </>
        )}
        .{installed && ` Installed ${installed}.`}
        {updated && updated !== installed && ` Updated ${updated}.`}
        {!source.managed && " Installed with the skills CLI."}
      </p>
    </section>
  );
}

/** Same-name copies in the one group: pick which one this view is about. */
function CopySwitcher({
  copies,
  current,
  onSwitch,
}: {
  copies: Skill[];
  current: string;
  onSwitch: (skill: Skill) => void;
}) {
  if (copies.length < 2) return null;
  return (
    <section aria-label="Copies" className="mb-4">
      <SectionHeading>{copies.length} copies of this name</SectionHeading>
      <ul className="divide-y rounded-lg border">
        {copies.map((copy) => {
          const selected = copy.dir === current;
          const seen = joinWords(copy.harnesses.map(harnessLabel));
          return (
            <li key={copy.dir}>
              <button
                type="button"
                aria-current={selected ? "true" : undefined}
                onClick={() => !selected && onSwitch(copy)}
                className={cn(
                  "focus-visible:ring-ring flex min-h-11 w-full flex-col justify-center gap-0.5 px-3 py-2 text-left outline-none focus-visible:ring-2",
                  selected ? "bg-accent" : "hover:bg-accent/50",
                )}
              >
                <span className="font-mono text-[11.5px] break-all">{copy.paths[0] ?? copy.dir}</span>
                <span className="text-muted-foreground text-[12px]">
                  {seen ? `Seen by ${seen}` : "No harness reads it"}
                  {selected ? " · showing" : ""}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

export function SkillDetailView({
  command,
  scopeArgs,
  skill,
  copies,
  startEditing,
  onBack,
  onChanged,
  onRemoved,
  onSwitchCopy,
  onUse,
  updateAction,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  skill: Skill;
  /** Every copy sharing this skill's row in the list, this one included. */
  copies?: Skill[];
  /** Open straight into the editor, as after creating the skill. */
  startEditing?: boolean;
  onBack: () => void;
  onChanged: (skill: Skill) => void;
  onRemoved: (skill: Skill) => void;
  onSwitchCopy?: (skill: Skill) => void;
  /** Writes the skill's token into the thread's composer. Absent outside a thread. */
  onUse?: (skill: Skill) => void | Promise<void>;
  /** The update flow's control, shown between Edit and Remove. */
  updateAction?: ReactNode;
}) {
  const [detail, setDetail] = useState<SkillDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reloadSeq, setReloadSeq] = useState(0);

  // null = SKILL.md; otherwise a file path relative to the skill.
  const [doc, setDoc] = useState<string | null>(null);
  const [mode, setMode] = useState<ViewMode>("preview");
  const [file, setFile] = useState<SkillFileContent | null>(null);
  const [fileLoading, setFileLoading] = useState(false);
  const [fileError, setFileError] = useState("");
  // Files already fetched this visit: flipping between two on 4G should not
  // cost a round trip each time.
  const fileCache = useRef(new Map<string, SkillFileContent>());

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const pendingEdit = useRef(Boolean(startEditing));

  const [using, setUsing] = useState(false);
  const [useError, setUseError] = useState("");
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState("");

  const scrollRef = useRef<HTMLDivElement>(null);
  const docRef = useRef<HTMLDivElement>(null);
  const { copy } = useCopy();

  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);
  const onChangedRef = useLatest(onChanged);

  // An update swaps the files under the same dir, and the record's timestamp
  // is the only sign of it that reaches here.
  const version = skill.source?.updatedAt ?? "";

  useEffect(() => {
    let stale = false;
    setLoading(true);
    setError("");
    fileCache.current.clear();
    commandRef
      .current<SkillDetail>("read_skill", { ...argsRef.current, dir: skill.dir })
      .then((raw) => {
        if (stale) return;
        const d = normalizeSkill(raw);
        setDetail(d);
        setLoading(false);
        if (pendingEdit.current) {
          pendingEdit.current = false;
          setDraft(d.content);
          setEditing(true);
        }
      })
      .catch((e) => {
        if (stale) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => {
      stale = true;
    };
  }, [skill.dir, version, reloadSeq, commandRef, argsRef]);

  useEffect(() => {
    if (doc === null) return;
    const cached = fileCache.current.get(doc);
    if (cached) {
      setFile(cached);
      setFileError("");
      return;
    }
    let stale = false;
    setFile(null);
    setFileLoading(true);
    setFileError("");
    commandRef
      .current<SkillFileContent>("read_skill_file", { ...argsRef.current, dir: skill.dir, path: doc })
      .then((f) => {
        if (stale) return;
        fileCache.current.set(doc, f);
        setFile(f);
        setFileLoading(false);
      })
      .catch((e) => {
        if (stale) return;
        setFileError(errorText(e));
        setFileLoading(false);
      });
    return () => {
      stale = true;
    };
  }, [doc, skill.dir, version, commandRef, argsRef]);

  // The document sits under the skill's facts, so opening a file has to bring
  // it to the top rather than leave the reader at the file list below it.
  const openDoc = (next: string | null) => {
    setDoc(next);
    const scroller = scrollRef.current;
    const heading = docRef.current;
    if (scroller && heading) scroller.scrollTop = Math.max(0, heading.offsetTop - scroller.offsetTop);
  };

  // The list's copy is the newer one after a toggle or a link; the detail's
  // is only newer for what the list does not carry (content, files).
  const current = skill;
  const dirty = editing && detail !== null && draft !== detail.content;

  const confirmDiscard = () => !dirty || window.confirm("Discard your unsaved changes?");

  const startEdit = () => {
    if (!detail) return;
    setDraft(detail.content);
    setSaveError("");
    openDoc(null);
    setEditing(true);
  };

  const cancelEdit = () => {
    if (!confirmDiscard()) return;
    setEditing(false);
    setSaveError("");
  };

  const save = useCallback(async () => {
    if (saving) return;
    setSaving(true);
    setSaveError("");
    try {
      await commandRef.current("save_skill", { ...argsRef.current, dir: skill.dir, content: draft });
      const fresh = normalizeSkill(
        await commandRef.current<SkillDetail>("read_skill", { ...argsRef.current, dir: skill.dir }),
      );
      setDetail(fresh);
      setEditing(false);
      onChangedRef.current(stripDetail(fresh));
    } catch (e) {
      setSaveError(errorText(e));
    } finally {
      setSaving(false);
    }
  }, [draft, saving, skill.dir, commandRef, argsRef, onChangedRef]);

  const back = () => {
    if (!confirmDiscard()) return;
    onBack();
  };

  const use = async () => {
    if (!onUse || using) return;
    setUsing(true);
    setUseError("");
    try {
      await onUse(current);
    } catch (e) {
      setUseError(errorText(e));
    } finally {
      setUsing(false);
    }
  };

  // Turning manual on or off rewrites SKILL.md's frontmatter and may create
  // agents/openai.yaml, so what was read before is stale.
  const invocationChanged = (next: Skill) => {
    fileCache.current.clear();
    setReloadSeq((n) => n + 1);
    onChangedRef.current(normalizeSkill(next));
  };

  const remove = async () => {
    if (removing) return;
    setRemoving(true);
    setRemoveError("");
    try {
      await commandRef.current("remove_skill", { ...argsRef.current, dir: skill.dir });
      setConfirmingRemove(false);
      onRemoved(current);
    } catch (e) {
      setRemoveError(errorText(e));
    } finally {
      setRemoving(false);
    }
  };

  const header = (
    <div className="flex items-start gap-1 border-b px-1 py-1">
      <IconButton label="Back to skills" onClick={back}>
        <ArrowLeftIcon />
      </IconButton>
      <div className="min-w-0 flex-1 py-1">
        <h2 className="min-w-0 truncate font-mono text-[14px] font-medium" title={current.name}>
          {current.name}
        </h2>
        <div className="mt-1 flex flex-wrap items-center gap-1.5">
          <OriginBadge skill={current} />
          {!current.editable && <span className="text-muted-foreground text-[11px]">read-only</span>}
        </div>
      </div>
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="More actions" className="size-11 shrink-0 md:size-8">
                <EllipsisVerticalIcon />
              </Button>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>More actions</TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem onSelect={() => void copy(current.dir)} className="min-h-11 gap-2 text-[13px] md:min-h-0">
            <CopyIcon className="size-3.5" />
            Copy path
          </DropdownMenuItem>
          <DropdownMenuItem
            onSelect={() => void copy(`${current.dir}/SKILL.md`)}
            className="min-h-11 gap-2 text-[13px] md:min-h-0"
          >
            <FileIcon className="size-3.5" />
            Copy SKILL.md path
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );

  const actionClass = "h-11 text-[13px] md:h-8 md:text-[12px]";
  const hasActions = Boolean(onUse) || current.editable || Boolean(updateAction);
  const actions = hasActions && (
    <div className="border-b px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        {onUse && (
          <Button size="sm" className={actionClass} onClick={() => void use()} disabled={using}>
            {using ? <Spinner className="size-3.5" /> : <CornerDownLeftIcon className="size-3.5" />}
            Use
          </Button>
        )}
        {current.editable && (
          <Button variant="outline" size="sm" className={actionClass} onClick={startEdit} disabled={!detail}>
            <PencilIcon className="size-3.5" />
            Edit
          </Button>
        )}
        {updateAction}
        {current.editable && (
          <Button
            variant="outline"
            size="sm"
            className={cn(actionClass, "text-destructive hover:text-destructive ml-auto")}
            onClick={() => {
              setRemoveError("");
              setConfirmingRemove(true);
            }}
          >
            <Trash2Icon className="size-3.5" />
            Remove
          </Button>
        )}
      </div>
      {useError && <ErrorLine message={useError} className="mt-2" />}
    </div>
  );

  if (editing) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {header}
        <div className="flex items-center gap-2 border-b px-3 py-1.5">
          <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[10.5px]">
            Editing SKILL.md{dirty ? " · unsaved" : ""}
          </span>
          <Button variant="ghost" size="sm" className="h-11 text-[12px] md:h-8" onClick={cancelEdit} disabled={saving}>
            Cancel
          </Button>
          <Button size="sm" className="h-11 text-[12px] md:h-8" onClick={() => void save()} disabled={saving || !dirty}>
            {saving && <Spinner className="size-3.5" />}
            Save
          </Button>
        </div>
        {saveError && <ErrorLine message={saveError} className="border-b px-3 py-2" />}
        <textarea
          aria-label="SKILL.md source"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
              e.preventDefault();
              if (dirty) void save();
            }
          }}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          // 16px on a phone so focusing it does not zoom Mobile Safari.
          className="bg-background placeholder:text-muted-foreground min-h-0 w-full flex-1 resize-none px-3 py-2 font-mono text-base leading-relaxed outline-none md:text-[12px]"
        />
      </div>
    );
  }

  const docPath = doc ?? "SKILL.md";
  const viewingText = doc === null ? (detail?.content ?? null) : file && !file.binary ? file.content : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {header}
      {actions}

      <div ref={scrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
        {current.problem && (
          <div className="bg-attention-surface mb-4 rounded-lg border px-3 py-2">
            <ProblemText problem={current.problem} className="text-[12.5px]" />
          </div>
        )}

        {onSwitchCopy && <CopySwitcher copies={copies ?? []} current={current.dir} onSwitch={onSwitchCopy} />}

        <InvocationSection skill={current} command={command} scopeArgs={scopeArgs} onChanged={invocationChanged} />

        {current.source && <SourceLine source={current.source} />}

        <div ref={docRef} className="mt-6 mb-2 flex items-center gap-2">
          <div className="flex min-w-0 flex-1 items-center gap-1">
            <span className="min-w-0 truncate font-mono text-[12px]" title={docPath}>
              {docPath}
            </span>
            {doc !== null && (
              <button
                type="button"
                onClick={() => openDoc(null)}
                aria-label="Back to SKILL.md"
                className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex size-11 shrink-0 items-center justify-center rounded-md outline-none focus-visible:ring-2 md:size-6"
              >
                <XIcon className="size-3.5" />
              </button>
            )}
          </div>
          {isMarkdown(docPath) && (
            <Segmented
              label="View"
              value={mode}
              onChange={setMode}
              className="shrink-0"
              options={[
                { id: "preview", label: "Preview" },
                { id: "source", label: "Source" },
              ]}
            />
          )}
        </div>

        {loading && !detail ? (
          <p className="text-muted-foreground flex items-center gap-2 py-4 text-[12px]">
            <Spinner className="text-primary size-3.5" /> Reading {current.name}…
          </p>
        ) : error && !detail ? (
          <div className="space-y-2 py-2">
            <ErrorLine message={error} />
            <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => setReloadSeq((n) => n + 1)}>
              Try again
            </Button>
          </div>
        ) : doc !== null && fileLoading ? (
          <p className="text-muted-foreground flex items-center gap-2 py-4 text-[12px]">
            <Spinner className="text-primary size-3.5" /> Reading {doc}…
          </p>
        ) : doc !== null && fileError ? (
          <ErrorLine message={fileError} className="py-2" />
        ) : doc !== null && file?.binary ? (
          <p className="text-muted-foreground py-4 text-[12px]">This is a binary file, so there is nothing to show as text.</p>
        ) : viewingText !== null ? (
          <DocumentView text={viewingText} path={docPath} mode={mode} />
        ) : null}

        {detail && detail.files.length > 0 && (
          <section className="mt-6">
            <SectionHeading>
              Files <span className="tabular-nums">{detail.files.length}</span>
            </SectionHeading>
            <ul className="-mx-1">
              {detail.files.map((f) => {
                const { Icon, tone } = fileIconFor(f.path);
                const selected = f.path === doc;
                return (
                  <li key={f.path}>
                    <button
                      type="button"
                      onClick={() => openDoc(selected ? null : f.path)}
                      aria-current={selected ? "true" : undefined}
                      className={cn(
                        "focus-visible:ring-ring group flex min-h-11 w-full items-center gap-2 rounded-md px-1 py-1 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0",
                        selected ? "bg-accent" : "hover:bg-accent/50",
                      )}
                    >
                      <Icon className={cn("size-3.5 shrink-0", tone || "text-muted-foreground/70")} />
                      <span className="text-muted-foreground/90 group-hover:text-foreground min-w-0 flex-1 truncate font-mono text-[11.5px]">
                        {f.path}
                      </span>
                      <span className="text-muted-foreground/70 shrink-0 text-[11px] tabular-nums">{fmtSize(f.size)}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>
        )}

        <section className="mt-6 mb-2">
          <SectionHeading>Found at</SectionHeading>
          <ul className="text-muted-foreground space-y-0.5 font-mono text-[11px] break-all">
            {current.paths.map((p) => (
              <li key={p}>{p}</li>
            ))}
            {!current.paths.includes(current.dir) && (
              <li className="text-muted-foreground/70">→ {current.dir}</li>
            )}
          </ul>
        </section>
      </div>

      <Dialog open={confirmingRemove} onOpenChange={(next) => !removing && setConfirmingRemove(next)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Remove {current.name}?</DialogTitle>
            <DialogDescription>
              This deletes the skill's folder and every link to it.
              {current.source
                ? " It can be installed again from its source."
                : " It was written here, so there is nowhere to get it back from."}
            </DialogDescription>
          </DialogHeader>
          <p className="text-muted-foreground font-mono text-[11.5px] break-all">{current.dir}</p>
          {removeError && <ErrorLine message={removeError} />}
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmingRemove(false)} disabled={removing}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void remove()} disabled={removing}>
              {removing && <Spinner className="size-3.5" />}
              Remove skill
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
