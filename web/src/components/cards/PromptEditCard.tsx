import { useMemo, useState } from "react";

import { DiffLines } from "~/components/Diff";
import { Button } from "~/components/ui/button";
import { Textarea } from "~/components/ui/textarea";
import { compactUse, harnessName } from "~/components/agents/agentText";
import { useAgentNav } from "~/lib/agentNav";
import { editableSpan, promptEditBody, revisionLabel } from "~/lib/cards";
import { diffLines } from "~/lib/lineDiff";
import type { Card } from "~/protocol";

import { CardFrame, Fact, type CardControl } from "./CardFrame";

/**
 * prompt_edit: an agent asking to change its own system prompt or compaction
 * instructions. The user's quoted words, the change as a diff, and when it
 * takes effect. Edit opens the changed lines for typing, the diff following
 * along, and the footer stays Reject and Approve.
 */
export function PromptEditCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const edit = card.promptEdit;
  const span = useMemo(() => (edit ? editableSpan(edit.before, edit.after) : null), [edit]);
  // The edited span, while Edit is open; null is the proposal as sent.
  const [draft, setDraft] = useState<string | null>(null);
  const body = draft === null ? undefined : promptEditBody(card, draft);
  const after = body ?? edit?.after ?? "";
  const lines = useMemo(() => diffLines(edit?.before ?? "", after), [edit?.before, after]);
  // The card sits in the agent's own thread, which is where its harness is known.
  const { current } = useAgentNav();
  if (!edit || !span) return null;

  const prompt = edit.kind === "prompt";
  const harness = current?.id === edit.agentId ? current.harness : undefined;
  // A prompt is read at process start, and so are Codex's compaction instructions.
  const use = prompt ? "start" : harness ? compactUse(harness) : "compaction";
  return (
    <CardFrame
      card={card}
      ctl={ctl}
      accept={body === undefined ? "Approve" : "Approve edited"}
      decline="Reject"
      edits={() => (body === undefined ? undefined : { body })}
    >
      {edit.reason && (
        <Fact label="You said">
          <blockquote className="text-muted-foreground border-l-2 pl-3 italic">“{edit.reason}”</blockquote>
        </Fact>
      )}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <p className="text-muted-foreground text-[11.5px] leading-none">Change</p>
          <Button
            type="button"
            variant="link"
            size="sm"
            disabled={ctl.locked}
            onClick={() => setDraft(draft === null ? span.middle : null)}
            className="text-muted-foreground hover:text-foreground h-auto p-0 text-[12px] pointer-coarse:min-h-11"
          >
            {draft === null ? "Edit" : "Undo"}
          </Button>
        </div>
        {draft !== null && (
          <Textarea
            aria-label="Edited change"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            disabled={ctl.locked}
            autoFocus
            className="max-h-[30dvh] min-h-20 font-mono text-[12px] md:text-[12px]"
          />
        )}
        <div
          role="group"
          aria-label={`${prompt ? "System prompt" : "Compaction instructions"} ${revisionLabel(edit.kind, edit.fromRev)} to ${revisionLabel(edit.kind, edit.fromRev + 1)}`}
          className="overflow-hidden rounded-lg border"
        >
          <p className="text-muted-foreground border-b px-2 py-1 font-mono text-[11px]">
            {prompt ? "System prompt" : "Compaction instructions"} {revisionLabel(edit.kind, edit.fromRev)} →{" "}
            {revisionLabel(edit.kind, edit.fromRev + 1)}
          </p>
          {/* Always unified and wrapped: this is prose, and a phone has no
              room for two columns of it. */}
          <DiffLines lines={lines} wrap />
        </div>
      </div>

      <Fact label="Takes effect">
        {use === "start" ? (
          <>
            When this turn ends. The thread restarts and keeps its transcript.{" "}
            <span className="text-muted-foreground">Running shells are killed.</span>
          </>
        ) : use === "ignored" ? (
          <>Never. {harnessName(harness!)} ignores compaction instructions.</>
        ) : (
          <>
            At the next compaction. <span className="text-muted-foreground">No restart.</span>
          </>
        )}
      </Fact>
    </CardFrame>
  );
}
