import { ChevronRightIcon } from "lucide-react";
import { useState } from "react";

import { DestinationPicker } from "~/components/skills/parts";
import { ProblemText } from "~/components/tools/parts";
import { Checkbox } from "~/components/ui/checkbox";
import { createEdits, installEdits, pickedFirst, startTicks } from "~/lib/cards";
import { fmtSize } from "~/lib/skills";
import { cn } from "~/lib/utils";
import type { Card, CardStagedSkill } from "~/protocol";

import { CardFrame, Code, Fact, HarnessFact, type CardControl } from "./CardFrame";

/** install_skill: what the source holds, which of it to install, and where. */
export function InstallCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const skills = pickedFirst(card.staged?.skills ?? []);
  const [ticked, setTicked] = useState<string[]>(() => startTicks(skills));
  const [destination, setDestination] = useState(card.destination ?? "");
  const [expanded, setExpanded] = useState<string | null>(null);
  const count = ticked.length;

  const tick = (name: string, on: boolean) =>
    setTicked((t) => (on ? [...t.filter((n) => n !== name), name] : t.filter((n) => n !== name)));

  return (
    <CardFrame
      card={card}
      ctl={ctl}
      accept={count > 1 ? `Install ${count}` : "Install"}
      canAccept={count > 0}
      edits={() => installEdits(card, ticked, destination)}
    >
      {card.staged?.source && (
        <Fact label="From">
          <Code>{card.staged.source}</Code>
        </Fact>
      )}

      <ul aria-label="Skills found" className="divide-y rounded-lg border">
        {skills.map((s) => (
          <StagedRow
            key={s.name}
            skill={s}
            ticked={ticked.includes(s.name)}
            onTick={(on) => tick(s.name, on)}
            open={expanded === s.name}
            onOpen={(open) => setExpanded(open ? s.name : null)}
            disabled={ctl.locked}
          />
        ))}
      </ul>

      <DestinationPicker
        label="Install into"
        destinations={card.destinations ?? []}
        value={destination}
        onChange={setDestination}
        disabled={ctl.locked}
      />
      <HarnessFact harnesses={card.harnesses} />
    </CardFrame>
  );
}

function StagedRow({
  skill,
  ticked,
  onTick,
  open,
  onOpen,
  disabled,
}: {
  skill: CardStagedSkill;
  ticked: boolean;
  onTick: (on: boolean) => void;
  open: boolean;
  onOpen: (open: boolean) => void;
  disabled: boolean;
}) {
  // Go sends an empty slice as null.
  const files = skill.files ?? [];
  return (
    <li className="px-1">
      <div className="flex items-start">
        {/* The box has a thumb-sized target of its own: the name beside it
            opens the file list. */}
        <label className="flex size-11 shrink-0 cursor-pointer items-center justify-center">
          <Checkbox
            aria-label={`Install ${skill.name}`}
            checked={ticked}
            disabled={disabled}
            onCheckedChange={(v) => onTick(v === true)}
            className="size-5 md:size-4"
          />
        </label>
        <button
          type="button"
          aria-expanded={open}
          onClick={() => onOpen(!open)}
          className="focus-visible:ring-ring flex min-h-11 min-w-0 flex-1 flex-col items-start gap-0.5 rounded-md py-2 pr-2 text-left outline-none focus-visible:ring-2"
        >
          <span className="flex w-full min-w-0 items-center gap-1.5">
            <span className="min-w-0 font-mono text-[13px] wrap-anywhere">{skill.name}</span>{" "}
            <span className="text-muted-foreground ml-auto shrink-0 text-[11.5px]">
              {files.length} {files.length === 1 ? "file" : "files"}
            </span>
            <ChevronRightIcon
              aria-hidden
              className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-90")}
            />
          </span>{" "}
          {skill.description && (
            <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">
              {skill.description}
            </span>
          )}
          <ProblemText problem={skill.problem} />
        </button>
      </div>
      {open && (
        <ul aria-label={`${skill.name} files`} className="mb-2 ml-11 space-y-0.5 pr-2">
          {files.map((f) => (
            <li key={f.path} className="flex items-baseline gap-2 text-[12px]">
              <Code className="min-w-0 flex-1">{f.path}</Code>
              <span className="text-muted-foreground shrink-0 text-[11px] tabular-nums">{fmtSize(f.size)}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

/** create_skill: what the agent wrote, read before it lands, and where. */
export function CreateCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const [destination, setDestination] = useState(card.destination ?? "");
  const [showContent, setShowContent] = useState(false);
  const skill = card.skill;

  return (
    <CardFrame card={card} ctl={ctl} accept="Install" edits={() => createEdits(card, destination)}>
      <Fact label="Skill">
        <Code className="text-[13px] font-medium">{skill?.name}</Code>
        {skill?.description && (
          <p className="text-muted-foreground mt-1 text-[12.5px] leading-snug">{skill.description}</p>
        )}
      </Fact>

      <div className="space-y-1.5">
        <button
          type="button"
          aria-expanded={showContent}
          onClick={() => setShowContent((v) => !v)}
          className="focus-visible:ring-ring -mx-1 flex min-h-11 items-center gap-1.5 rounded-md px-1 text-[12.5px] outline-none focus-visible:ring-2 md:min-h-8"
        >
          <ChevronRightIcon
            aria-hidden
            className={cn("text-muted-foreground size-3.5 transition-transform", showContent && "rotate-90")}
          />
          <span className="font-mono text-[12px]">SKILL.md</span>
          <span className="text-muted-foreground text-[11.5px]">
            {(skill?.content ?? "").split("\n").length} lines
          </span>
        </button>
        {showContent && (
          // Source, not rendered markdown: this is where the reader decides
          // whether to trust what the agent wrote.
          <pre
            aria-label="SKILL.md"
            className="bg-muted/30 scroll-thin max-h-[40dvh] overflow-auto overscroll-contain rounded-lg border px-3 py-2 font-mono text-[11.5px] leading-relaxed break-words whitespace-pre-wrap"
          >
            {skill?.content}
          </pre>
        )}
      </div>

      <DestinationPicker
        label="Create in"
        destinations={card.destinations ?? []}
        value={destination}
        onChange={setDestination}
        disabled={ctl.locked}
      />
      <HarnessFact harnesses={card.harnesses} />
    </CardFrame>
  );
}
