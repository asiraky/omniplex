import { CheckIcon, ChevronRightIcon, CopyIcon, RotateCwIcon } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";

import { Markdown } from "~/components/Markdown";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "~/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Separator } from "~/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { useCopy } from "~/lib/clipboard";
import { cn } from "~/lib/utils";
import type { SessionSummary, UserConfig } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

/**
 * What a summary is for: you opened a session you started days ago and cannot
 * remember what you asked it. Scrolling to the top of the transcript to reread
 * your own rambling prompt, then to the bottom to find out how it ended, is
 * the thing this replaces.
 *
 * The prompt editor lives in here rather than in settings because this is
 * where you find out the prompt is wrong. A summary that asked the wrong
 * question is the only reason to change it, and the fix is one panel away
 * instead of behind a project you may not even have.
 */
export function SessionSummaryPanel({
  summary,
  loading,
  error,
  stale,
  userConfig,
  onRegenerate,
  onSavePrompt,
  onClose,
}: {
  summary: SessionSummary | null;
  loading: boolean;
  error: string | null;
  /** True when the session has moved on since this summary was made. */
  stale: boolean;
  userConfig: UserConfig | null;
  onRegenerate: () => void;
  onSavePrompt: (prompt: string) => Promise<void>;
  onClose: () => void;
}) {
  const isDesktop = useIsDesktop();
  const title = "Session summary";
  const description = "What you asked for, what the agent did, and what is left.";
  const onOpenChange = (open: boolean) => !open && onClose();

  const body: ReactNode = (
    <SummaryBody
      summary={summary}
      loading={loading}
      error={error}
      stale={stale}
      userConfig={userConfig}
      onRegenerate={onRegenerate}
      onSavePrompt={onSavePrompt}
    />
  );

  // On a phone this arrives from the bottom, where a thumb already is; on a
  // pointer it is an ordinary centred dialog. Same content either way.
  if (!isDesktop) {
    return (
      <Sheet open onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          className="scroll-thin max-h-[85dvh] overflow-y-auto pb-[calc(1rem+env(safe-area-inset-bottom))]"
        >
          <SheetHeader>
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{description}</SheetDescription>
          </SheetHeader>
          <div className="space-y-3 px-4 pb-4">{body}</div>
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="scroll-thin max-h-[85dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">{body}</div>
      </DialogContent>
    </Dialog>
  );
}

function SummaryBody({
  summary,
  loading,
  error,
  stale,
  userConfig,
  onRegenerate,
  onSavePrompt,
}: {
  summary: SessionSummary | null;
  loading: boolean;
  error: string | null;
  stale: boolean;
  userConfig: UserConfig | null;
  onRegenerate: () => void;
  onSavePrompt: (prompt: string) => Promise<void>;
}) {
  const { copied, copy } = useCopy();

  return (
    <>
      {loading && (
        <div className="text-muted-foreground flex items-center gap-2 py-6 text-[13px]">
          <Spinner />
          {/* Naming the model explains the wait: this is a process start, not
              a request to something already running. */}
          <span>Reading the transcript…</span>
        </div>
      )}

      {!loading && error && (
        <Alert variant="destructive">
          <AlertDescription className="text-[13px]">{error}</AlertDescription>
        </Alert>
      )}

      {!loading && !error && summary && (
        <>
          {stale && (
            <p className="text-muted-foreground text-[11px]">
              The session has moved on since this was written.
            </p>
          )}
          <Markdown text={summary.text} className="text-[13px] leading-relaxed" />
          <Separator />
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted-foreground flex-1 text-[11px]">
              {summary.harness}
              {summary.model && ` · ${summary.model}`}
            </span>
            <Button variant="ghost" size="sm" onClick={() => copy(summary.text)}>
              {copied ? <CheckIcon /> : <CopyIcon />}
              {copied ? "Copied" : "Copy"}
            </Button>
            <Button variant="outline" size="sm" onClick={onRegenerate}>
              <RotateCwIcon />
              Summarise again
            </Button>
          </div>
        </>
      )}

      {!loading && (
        <PromptEditor
          value={userConfig?.summaryPrompt ?? ""}
          onSave={onSavePrompt}
          onSaved={onRegenerate}
        />
      )}
    </>
  );
}

/**
 * The summarisation prompt, editable in place.
 *
 * Clearing the box is how you get the default back — the server substitutes it
 * for an empty value — so "Reset to default" empties the field and saves,
 * rather than this file holding a second copy of the default text that would
 * drift from the Go one.
 */
function PromptEditor({
  value,
  onSave,
  onSaved,
}: {
  value: string;
  onSave: (prompt: string) => Promise<void>;
  onSaved: () => void;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Track the saved value while the editor is closed. Reopening should show
  // what is on disk, not a draft abandoned three summaries ago — but typing
  // must not be clobbered by an unrelated config refresh, so this only syncs
  // while closed.
  useEffect(() => {
    if (!open) setDraft(value);
  }, [value, open]);

  const dirty = draft !== value;

  // A save that fails must not be followed by a re-summarise: that would run
  // against the old prompt and read as success.
  const save = async (next: string) => {
    setSaving(true);
    setSaveError(null);
    try {
      await onSave(next);
      onSaved();
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-[12px]">
        <ChevronRightIcon className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        Summarisation prompt
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-2 pt-2">
        <p className="text-muted-foreground text-[11px]" id={`${id}-hint`}>
          The instructions the summariser follows. Saved for this machine, and used
          for every session.
        </p>
        <Textarea
          id={id}
          aria-describedby={`${id}-hint`}
          aria-label="Summarisation prompt"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="scroll-thin max-h-64 min-h-32 text-[12px]"
        />
        <div className="flex items-center gap-2">
          <Button size="sm" disabled={!dirty || saving} onClick={() => save(draft)}>
            {saving && <Spinner />}
            Save and summarise again
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={saving}
            onClick={() => {
              setDraft("");
              void save("");
            }}
          >
            Reset to default
          </Button>
        </div>
        {saveError && (
          <Alert variant="destructive">
            <AlertDescription className="text-[12px]">{saveError}</AlertDescription>
          </Alert>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}
