import { ChevronRightIcon, PencilIcon, RefreshCwIcon, Trash2Icon, XIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Markdown } from "~/components/Markdown";
import {
  ConfirmDialog,
  DetailHeader,
  EditStrip,
  ErrorLine,
  ProblemText,
  Segmented,
  type PageCommand,
} from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Spinner } from "~/components/ui/spinner";
import { fileIconFor } from "~/lib/fileIcons";
import {
  fmtSize,
  MODE_LABEL,
  MODE_TEXT,
  originText,
  splitFrontmatter,
  type Skill,
  type SkillDetail,
  type SkillFileContent,
  type SkillMode,
} from "~/lib/skills";
import { cn, errorText } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { UpdateCheck } from "./UpdateCheck";

type ViewMode = "preview" | "source";

const MODES: { id: SkillMode; label: string }[] = (["on", "manual", "off"] as const).map((id) => ({
  id,
  label: MODE_LABEL[id],
}));

const isMarkdown = (path: string) => /\.(md|markdown|mdx)$/i.test(path);

function stripDetail(d: SkillDetail): Skill {
  const { content: _content, files: _files, ...skill } = d;
  return skill;
}

/**
 * SKILL.md's frontmatter as a compact key/value block above the body, less
 * the name and description the header already shows.
 */
function FrontmatterBlock({ fields }: { fields: [string, string][] }) {
  const rest = fields.filter(([key]) => key !== "name" && key !== "description");
  if (rest.length === 0) return null;
  return (
    <dl className="bg-muted/40 mb-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-lg border px-3 py-2">
      {rest.map(([key, value], i) => (
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

/**
 * On / Manual / Off for a skill that is ours to change. The tap shows at once
 * and goes back if the write fails: on a slow link, waiting for the server
 * before the switch moves reads as a missed tap.
 */
function ModeSwitch({
  skill,
  command,
  scopeArgs,
  onChanged,
}: {
  skill: Skill;
  command: PageCommand;
  scopeArgs: Record<string, unknown>;
  onChanged: (skill: Skill) => void;
}) {
  const [pending, setPending] = useState<SkillMode | null>(null);
  const [error, setError] = useState("");
  const shown = pending ?? skill.mode;

  const change = (mode: SkillMode) => {
    if (pending || mode === skill.mode) return;
    setPending(mode);
    setError("");
    command<Skill>("set_skill_mode", { ...scopeArgs, dir: skill.dir, mode })
      .then(onChanged)
      .catch((e) => setError(errorText(e)))
      .finally(() => setPending(null));
  };

  return (
    <div>
      <div className="flex items-center gap-2">
        <Segmented
          radio
          label="When agents use it"
          value={shown}
          options={MODES}
          onChange={change}
          disabled={pending !== null}
          className="w-full max-w-xs"
        />
        {pending && <Spinner className="size-3.5 shrink-0" />}
      </div>
      <p className="text-muted-foreground mt-1.5 px-1 text-[12.5px] leading-snug">{MODE_TEXT[shown]}</p>
      {error && <ErrorLine message={error} className="mt-1.5 px-1" />}
    </div>
  );
}

export function SkillDetailView({
  command,
  scopeArgs,
  skill,
  startEditing,
  onBack,
  onChanged,
  onRemoved,
}: {
  command: PageCommand;
  scopeArgs: Record<string, unknown>;
  skill: Skill;
  /** Open straight into the editor, as after creating the skill. */
  startEditing?: boolean;
  onBack: () => void;
  /** Skills the server just returned after a write to this one. */
  onChanged: (skills: Skill[]) => void;
  onRemoved: (skill: Skill) => void;
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

  // Bumped on every "Check for update", so each press is a fresh check.
  const [updateSeq, setUpdateSeq] = useState(0);
  const [confirmingRemove, setConfirmingRemove] = useState(false);

  const scrollRef = useRef<HTMLDivElement>(null);
  const docRef = useRef<HTMLDivElement>(null);

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
      .then((d) => {
        if (stale) return;
        setDetail({ ...d, files: d.files ?? [] });
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

  // The document sits under the buttons, so opening a file has to bring it
  // to the top rather than leave the reader at the file list below it.
  const openDoc = (next: string | null) => {
    setDoc(next);
    const scroller = scrollRef.current;
    const heading = docRef.current;
    if (scroller && heading) scroller.scrollTop = Math.max(0, heading.offsetTop - scroller.offsetTop);
  };

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
      const fresh = await commandRef.current<SkillDetail>("read_skill", { ...argsRef.current, dir: skill.dir });
      setDetail({ ...fresh, files: fresh.files ?? [] });
      setEditing(false);
      onChangedRef.current([stripDetail(fresh)]);
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

  // Manual lives in SKILL.md's frontmatter, so a mode change can rewrite
  // what was read before.
  const modeChanged = (next: Skill) => {
    fileCache.current.clear();
    setReloadSeq((n) => n + 1);
    onChangedRef.current([next]);
  };

  const remove = async () => {
    await commandRef.current("remove_skill", { ...argsRef.current, dir: skill.dir });
    onRemoved(skill);
  };

  const header = <DetailHeader backLabel="Back to skills" onBack={back} title={skill.name} sub={originText(skill)} />;

  if (editing) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {header}
        <EditStrip
          label={`Editing SKILL.md${dirty ? " · unsaved" : ""}`}
          saving={saving}
          canSave={dirty}
          onCancel={cancelEdit}
          onSave={() => void save()}
        />
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
  const actionClass = "h-11 text-[13px] md:h-8 md:text-[12px]";
  const hasButtons = skill.editable || Boolean(skill.source);
  const files = detail?.files ?? [];

  return (
    <div className="flex h-full min-h-0 flex-col">
      {header}

      <div ref={scrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
        {skill.problem && (
          <div className="bg-attention-surface mb-4 rounded-lg border px-3 py-2">
            <ProblemText problem={skill.problem} className="text-[12.5px]" />
          </div>
        )}

        {skill.editable ? (
          <ModeSwitch skill={skill} command={command} scopeArgs={scopeArgs} onChanged={modeChanged} />
        ) : (
          skill.mode !== "on" && (
            <p className="px-1 text-[12.5px] leading-snug">
              {MODE_LABEL[skill.mode]}. <span className="text-muted-foreground">{MODE_TEXT[skill.mode]}</span>
            </p>
          )
        )}

        {hasButtons && (
          <div className="mt-4">
            <div className="flex flex-wrap items-center gap-2">
              {skill.editable && (
                <Button variant="outline" size="sm" className={actionClass} onClick={startEdit} disabled={!detail}>
                  <PencilIcon className="size-3.5" />
                  Edit
                </Button>
              )}
              {skill.source && (
                <Button variant="outline" size="sm" className={actionClass} onClick={() => setUpdateSeq((n) => n + 1)}>
                  <RefreshCwIcon className="size-3.5" />
                  Check for update
                </Button>
              )}
              {skill.editable && (
                <Button
                  variant="outline"
                  size="sm"
                  className={cn(actionClass, "text-destructive hover:text-destructive ml-auto")}
                  onClick={() => setConfirmingRemove(true)}
                >
                  <Trash2Icon className="size-3.5" />
                  Remove
                </Button>
              )}
            </div>
            {updateSeq > 0 && skill.source && (
              <UpdateCheck
                key={updateSeq}
                command={command}
                scopeArgs={scopeArgs}
                skill={skill}
                onUpdated={(skills) => onChangedRef.current(skills)}
              />
            )}
          </div>
        )}

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
            <Spinner className="text-primary size-3.5" /> Reading {skill.name}…
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

        {files.length > 0 && (
          <Collapsible className="mt-6 mb-2">
            <CollapsibleTrigger className="text-muted-foreground hover:text-foreground focus-visible:ring-ring group flex min-h-11 items-center gap-1.5 rounded-md text-[11px] font-semibold tracking-wide uppercase outline-none focus-visible:ring-2 md:min-h-8">
              <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-90" />
              Files <span className="tabular-nums">({files.length})</span>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <ul className="-mx-1">
                {files.map((f) => {
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
            </CollapsibleContent>
          </Collapsible>
        )}
      </div>

      <ConfirmDialog
        open={confirmingRemove}
        onOpenChange={setConfirmingRemove}
        title={`Remove ${skill.name}?`}
        description={
          <>
            This deletes the skill's folder.
            {skill.source
              ? ` You can install it again from ${skill.source.repo}.`
              : " It was written here, so there is no other copy to get it back from."}
          </>
        }
        confirmLabel="Remove skill"
        onConfirm={remove}
      />
    </div>
  );
}
