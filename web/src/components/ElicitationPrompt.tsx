import { CheckIcon, ExternalLinkIcon } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";

import { isEditable, keyboardLayerOpen } from "~/app/useKeybindings";
import { AttentionLabel } from "~/components/AttentionLabel";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Switch } from "~/components/ui/switch";
import {
  allowsOther,
  answersOf,
  elicitationDrafts,
  initialDraft,
  isMultiSelect,
  optionsOf,
  pickOption,
  pickOther,
  type ElicitationDraft,
  type ElicitationField,
  type FieldDraft,
} from "~/lib/elicitationDraft";
import { isComposing } from "~/lib/ime";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";
import type { PendingElicitation } from "~/protocol";

interface Props {
  threadId: string;
  request: PendingElicitation;
  onResolve(action: "accept" | "decline" | "cancel", value?: unknown): void;
}

// Long enough to see the row light up under a thumb before the next question
// replaces it; short enough not to feel like waiting.
const ADVANCE_MS = 180;

/**
 * The agent's questions, one at a time. Each field of the schema is a step:
 * a choice is a list of rows to tap (or a digit to press on a keyboard), and
 * picking one of a single-select moves on to the next question by itself. The
 * answers so far live outside the component, so switching threads and back
 * finds them where they were left.
 */
export function ElicitationPrompt({ threadId, request, onResolve }: Props) {
  const properties = request.schema?.properties ?? {};
  const keys = Object.keys(properties);
  const q = useQuestions(threadId, request.requestId, properties);
  const key = keys[q.step];
  const field = key ? properties[key] : undefined;
  const last = q.step >= keys.length - 1;

  useDigitKeys((n) => {
    const step = q.settle();
    const field = properties[keys[step]];
    if (!field) return false;
    const options = optionsOf(field);
    if (n <= options.length) q.pick(step, options[n - 1]);
    else if (allowsOther(field) && n === options.length + 1) q.other(step);
    else return false;
    return true;
  });

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (last) onResolve("accept", q.answers());
    else q.goTo(q.step + 1);
  };

  return (
    // Like the permission card, this blocks the turn. It floats in the band
    // above the composer as a rounded card matching the input, rather than a
    // full-bleed coloured tray, so it reads as part of the transcript's tail.
    <div className="mx-auto max-w-3xl px-4 pb-2.5 md:px-5">
      <form
        onSubmit={submit}
        aria-label="Input requested"
        className="attention-in bg-card ring-attention/25 flex max-h-[60dvh] flex-col rounded-2xl border shadow-lg ring-1"
      >
        {/* The question scrolls; the actions do not, so Next and Continue
            are always in reach however long the option list runs. */}
        <div className="overflow-y-auto overscroll-contain p-4">
          <AttentionLabel label="input needed">
            {keys.length > 1 && (
              <span className="text-muted-foreground ml-auto font-mono text-[10px] tabular-nums">
                {q.step + 1} of {keys.length}
              </span>
            )}
          </AttentionLabel>
          <p className="mt-2.5 mb-3 text-[13px]">{request.prompt || "Input requested"}</p>

          {request.schema?.["x-url"] && (
            <a
              href={request.schema["x-url"]}
              target="_blank"
              rel="noreferrer"
              className="text-primary mb-3 inline-flex items-center gap-1.5 break-all text-[12px] underline underline-offset-4"
            >
              {request.schema["x-url"]}
              <ExternalLinkIcon className="size-3 shrink-0" />
            </a>
          )}

          {key && field && q.draft.fields[key] && (
            // Keyed by the question so a text box's focus and a row's hover
            // do not carry over to the next one.
            <Question
              key={key}
              name={key}
              field={field}
              required={Boolean(request.schema?.required?.includes(key))}
              draft={q.draft.fields[key]}
              focus={q.moved}
              onPick={(option) => q.pick(q.step, option)}
              onOther={() => q.other(q.step)}
              onChange={(update) => q.update(q.step, update)}
            />
          )}
        </div>

        <div className="flex items-center gap-2 border-t px-4 py-3">
          <Button type="button" variant="ghost" onClick={() => onResolve("decline")}>
            Decline
          </Button>
          <div className="flex-1" />
          {q.step > 0 && (
            <Button type="button" variant="outline" onClick={() => q.goTo(q.step - 1)}>
              Back
            </Button>
          )}
          <Button type="submit">{last ? "Continue" : "Next"}</Button>
        </div>
      </form>
    </div>
  );
}

/**
 * The answers and the question on screen, saved as they change so a remount
 * picks them up. Every write goes through `latest` synchronously as well, so a
 * key pressed or a timer fired before the rerender sees it.
 */
function useQuestions(
  threadId: string,
  requestId: string,
  properties: Record<string, ElicitationField>,
) {
  const keys = Object.keys(properties);
  const lastStep = Math.max(keys.length - 1, 0);
  const [draft, setDraftState] = useState<ElicitationDraft>(
    () => elicitationDrafts.get(requestId) ?? initialDraft(properties),
  );
  const latest = useRef(draft);
  const save = (next: ElicitationDraft) => {
    latest.current = next;
    elicitationDrafts.set(threadId, requestId, next);
    setDraftState(next);
  };

  // Set once the human moves between questions, so the next question's text
  // box takes focus. Not on first show: that would steal focus from the
  // composer and, on a phone, throw up the keyboard over the thread.
  const [moved, setMoved] = useState(false);

  // The step a single-select pick is about to move on to, while it waits.
  const advancing = useRef<{ to: number; timer: number } | null>(null);
  const cancelAdvance = () => {
    if (advancing.current) window.clearTimeout(advancing.current.timer);
    advancing.current = null;
  };
  useEffect(() => cancelAdvance, []);

  const goTo = (step: number) => {
    cancelAdvance();
    setMoved(true);
    save({ ...latest.current, step: Math.min(Math.max(step, 0), lastStep) });
  };

  const update = (step: number, fn: (d: FieldDraft) => FieldDraft) => {
    const key = keys[step];
    const d = latest.current;
    if (!key || !d.fields[key]) return;
    save({ ...d, fields: { ...d.fields, [key]: fn(d.fields[key]) } });
  };

  return {
    draft,
    step: Math.min(draft.step, lastStep),
    moved,
    goTo,
    update,
    answers: () => answersOf(properties, latest.current),
    /**
     * The question a key press is for. In the moment between a pick and the
     * move it makes, that is the next question already: two digits typed
     * fast answer two questions, not the first one twice.
     */
    settle() {
      if (advancing.current) goTo(advancing.current.to);
      return Math.min(latest.current.step, lastStep);
    },
    // A single-select answered by an option is done; anything else (several
    // picks, an "Other" still to type) waits for Next.
    pick(step: number, option: string) {
      const field = properties[keys[step]];
      if (!field) return;
      cancelAdvance();
      update(step, (d) => pickOption(field, d, option));
      if (!isMultiSelect(field) && step < lastStep) {
        const to = step + 1;
        advancing.current = { to, timer: window.setTimeout(() => goTo(to), ADVANCE_MS) };
      }
    },
    other(step: number) {
      const field = properties[keys[step]];
      if (!field) return;
      cancelAdvance();
      setMoved(true);
      update(step, (d) => pickOther(field, d));
    },
  };
}

/**
 * Bare digits 1–9 for the question's rows, on a keyboard. Not while typing,
 * nor under a dialog or menu that owns the keyboard. The composer is the one
 * text box let through, while it is empty: it keeps focus after a send, which
 * is just when a question arrives, and with nothing in it a lone digit can
 * only be an answer. `onDigit` says whether the digit was used.
 */
function useDigitKeys(onDigit: (n: number) => boolean) {
  const latest = useLatest(onDigit);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat || isComposing(e)) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (!/^[1-9]$/.test(e.key)) return;
      const el = document.activeElement;
      const emptyComposer =
        el instanceof HTMLTextAreaElement && el.matches("[data-composer-input]") && el.value === "";
      if ((!emptyComposer && isEditable(el)) || keyboardLayerOpen()) return;
      if (latest.current(Number(e.key))) e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [latest]);
}

function Question({
  name,
  field,
  required,
  draft,
  focus,
  onPick,
  onOther,
  onChange,
}: {
  name: string;
  field: ElicitationField;
  required: boolean;
  draft: FieldDraft;
  /** Take focus on show: the human moved here, rather than it appearing. */
  focus: boolean;
  onPick(option: string): void;
  onOther(): void;
  onChange(update: (d: FieldDraft) => FieldDraft): void;
}) {
  const id = `${useId()}-${name}`;
  const labelId = `${id}-label`;
  const numeric = field.type === "number" || field.type === "integer";

  return (
    <div className="space-y-2.5">
      {/* The Claude bridge sends the question as the title and its short
          header as the description. */}
      <div className="space-y-1">
        <Label id={labelId} htmlFor={id} className="block text-[13px] leading-snug">
          {field.title || name}
        </Label>
        {field.description && (
          <p className="text-muted-foreground text-[11px] leading-snug">{field.description}</p>
        )}
      </div>

      {field.enum ? (
        <Choices
          id={id}
          labelId={labelId}
          field={field}
          draft={draft}
          focusOther={focus}
          onPick={onPick}
          onOther={onOther}
          onOtherText={(otherText) => onChange((d) => ({ ...d, otherText }))}
        />
      ) : field.type === "boolean" ? (
        <div className="flex h-9 items-center">
          <Switch
            id={id}
            checked={Boolean(draft.value)}
            onCheckedChange={(value) => onChange((d) => ({ ...d, value }))}
          />
        </div>
      ) : (
        <Input
          id={id}
          autoFocus={focus}
          type={field.format === "password" ? "password" : numeric ? "number" : "text"}
          required={required}
          value={String(draft.value ?? "")}
          onChange={(event) => {
            const value = numeric ? Number(event.target.value) : event.target.value;
            onChange((d) => ({ ...d, value }));
          }}
        />
      )}
    </div>
  );
}

/**
 * A choice field as full-width rows, each the whole hit target, numbered for
 * the digit keys. A single-select is a radio group; the `x-` hints the Claude
 * AskUserQuestion bridge sets make it a set of checkboxes (`x-multiSelect`),
 * add a free-text row (`x-allowOther`) and put each option's rationale under
 * its label (`x-optionDescriptions`).
 */
function Choices({
  id,
  labelId,
  field,
  draft,
  focusOther,
  onPick,
  onOther,
  onOtherText,
}: {
  id: string;
  labelId: string;
  field: ElicitationField;
  draft: FieldDraft;
  focusOther: boolean;
  onPick(option: string): void;
  onOther(): void;
  onOtherText(text: string): void;
}) {
  const options = optionsOf(field);
  const descriptions = (field["x-optionDescriptions"] ?? {}) as Record<string, string>;
  const multi = isMultiSelect(field);
  const other = allowsOther(field);
  const role = multi ? "checkbox" : "radio";

  return (
    <div className="space-y-1.5">
      {multi && <p className="text-muted-foreground text-[11px]">Pick any that apply.</p>}
      <div
        id={id}
        role={multi ? "group" : "radiogroup"}
        aria-labelledby={labelId}
        className="-mx-1.5 space-y-0.5"
      >
        {options.map((option, i) => (
          <ChoiceRow
            key={option}
            role={role}
            n={i + 1}
            checked={draft.picked.includes(option)}
            onClick={() => onPick(option)}
          >
            {option}
            {descriptions[option] && (
              <span className="text-muted-foreground mt-0.5 block text-[11px] leading-snug text-pretty">
                {descriptions[option]}
              </span>
            )}
          </ChoiceRow>
        ))}
        {other && (
          <ChoiceRow role={role} n={options.length + 1} checked={draft.otherOn} onClick={onOther}>
            Other
          </ChoiceRow>
        )}
      </div>
      {other && draft.otherOn && (
        <Input
          aria-label="Other answer"
          placeholder="Your answer"
          autoFocus={focusOther}
          value={draft.otherText}
          onChange={(e) => onOtherText(e.target.value)}
        />
      )}
    </div>
  );
}

function ChoiceRow({
  role,
  n,
  checked,
  onClick,
  children,
}: {
  role: "radio" | "checkbox";
  n: number;
  checked: boolean;
  onClick(): void;
  children: ReactNode;
}) {
  const multi = role === "checkbox";
  return (
    <button
      type="button"
      role={role}
      aria-checked={checked}
      onClick={onClick}
      className={cn(
        "focus-visible:ring-ring/50 flex min-h-11 w-full cursor-pointer items-start gap-2.5 rounded-lg px-1.5 py-2 text-left text-[13px] outline-none focus-visible:ring-[3px] md:min-h-0 md:py-1.5",
        checked ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      {/* The row's number is its digit key. A multi-select's box is square
          and takes a tick, so it reads as one of several. */}
      <span
        aria-hidden
        className={cn(
          "mt-px flex size-5 shrink-0 items-center justify-center border font-mono text-[10px] tabular-nums",
          multi ? "rounded-[5px]" : "rounded-full",
          checked
            ? "bg-primary border-primary text-primary-foreground"
            : "text-muted-foreground border-border",
        )}
      >
        {multi && checked ? <CheckIcon className="size-3" /> : n <= 9 ? n : null}
      </span>
      <span className="min-w-0 flex-1 leading-snug">{children}</span>
    </button>
  );
}
