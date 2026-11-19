import { useMemo, useState, type ReactNode } from "react";

import { ModelPicker } from "~/components/ModelPicker";
import { Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
import { Textarea } from "~/components/ui/textarea";
import type { AgentApi, AgentPatch } from "~/lib/agentApi";
import type { AgentTab } from "~/lib/agentNav";
import { formatEffort } from "~/lib/efforts";
import { toast } from "~/lib/toast";
import { cn, errorText } from "~/lib/utils";
import type { Agent, HarnessMeta, Project } from "~/protocol";
import { fmtK, harnessModes, keepEffort, modelEfforts, onlyInstance } from "./agentForm";

const WHOLE = "__whole";
const NEXT_MESSAGE = "Saved. Applies from the next message.";

/**
 * The agent's settings, one row each, saved as soon as each changes. Every row
 * says when its change takes effect. Scope and workspace cannot move a
 * running session, so they wait for a new thread and offer to start one.
 * Compact at is only linked: it is edited on the Compaction tab, in one place.
 */
export function AgentSettingsTab({
  agent,
  api,
  projects,
  harnesses,
  onOpenTab,
  onNewThread,
  onArchive,
}: {
  agent: Agent;
  api: AgentApi;
  projects: Project[];
  harnesses: HarnessMeta[];
  onOpenTab: (tab: AgentTab) => void;
  onNewThread: () => void;
  onArchive: () => void;
}) {
  // What the user just chose, shown until the agents frame carries it back,
  // so a select does not snap back to the old value for a round trip.
  const [over, setOver] = useState<AgentPatch>({});
  const [seen, setSeen] = useState(agent);
  if (seen !== agent) {
    setSeen(agent);
    setOver({});
  }
  // A scope or workspace change made while a thread runs, waiting for the next.
  const [waiting, setWaiting] = useState(false);
  const a = { ...agent, ...over };

  const save = async (patch: AgentPatch, done = "Saved") => {
    setOver((o) => ({ ...o, ...patch }));
    try {
      await api.save(agent.id, patch);
      toast.success(done);
      return true;
    } catch (e) {
      setOver((o) => {
        const next = { ...o };
        for (const k of Object.keys(patch)) delete next[k as keyof AgentPatch];
        return next;
      });
      toast.error(errorText(e));
      return false;
    }
  };
  const saveWhere = async (patch: AgentPatch) => {
    if ((await save(patch)) && agent.threadId) setWaiting(true);
  };

  const project = projects.find((p) => p.id === agent.projectId);
  const own = useMemo(
    () => onlyInstance(harnesses, agent.harness, agent.instance ?? ""),
    [harnesses, agent.harness, agent.instance],
  );
  const sel = { harness: agent.harness, instance: agent.instance ?? "", model: a.model ?? "" };
  const efforts = modelEfforts(own, sel);
  const modes = harnessModes(harnesses, agent.harness);
  const mode = modes.find((m) => m.id === a.mode) ?? modes.find((m) => m.default) ?? modes[0];
  const folders = project?.folders ?? [];
  const folder = folders.find((f) => f.id === a.folderId) ?? folders[0];

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-5 md:px-8 md:py-6">
      <Row title="Name" hint="Shown in the sidebar and the thread header.">
        <NameInput name={agent.name} onSave={(name) => void save({ name })} />
      </Row>
      <Row title="Model" hint="Applies from the next message. The composer's model picker changes the same setting.">
        <ModelPicker
          harnesses={own}
          lockDriver
          value={sel}
          onChange={(next) => void save({ model: next.model, effort: keepEffort(own, next, a.effort ?? "") }, NEXT_MESSAGE)}
          className="h-11 w-full md:h-9"
        />
      </Row>
      {efforts.length > 0 && (
        <Row title="Effort" hint="Applies from the next message.">
          <Segmented
            label="Effort"
            radio
            value={a.effort ?? ""}
            options={["", ...efforts].map((e) => ({ id: e, label: formatEffort(e) }))}
            onChange={(effort) => void save({ effort }, NEXT_MESSAGE)}
          />
        </Row>
      )}
      {modes.length > 0 && (
        <Row
          title="Permissions"
          hint={`${mode?.description ? `${mode.description.replace(/\.$/, "")}. ` : ""}Applies from the next message.`}
        >
          <Select value={mode?.id ?? ""} onValueChange={(v) => void save({ mode: v }, NEXT_MESSAGE)}>
            <SelectTrigger aria-label="Permissions" className="h-11 w-full md:h-9">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {modes.map((m) => (
                <SelectItem key={m.id} value={m.id}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      )}
      {folders.length > 1 && (
        <Row
          title="Scope"
          hint="Which folder of the project it works in. A running session cannot move, so this applies at the next new thread."
        >
          <Select
            value={a.folderId || WHOLE}
            onValueChange={(v) => void saveWhere({ folderId: v === WHOLE ? "" : v })}
          >
            <SelectTrigger aria-label="Scope" className="h-11 w-full md:h-9">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={WHOLE}>Everything</SelectItem>
              {folders.map((f) => (
                <SelectItem key={f.id} value={f.id}>
                  {f.path}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      )}
      <Row
        title="Workspace"
        hint={a.workspace === "managed" ? "Its own worktree, new for each thread." : `Main checkout${folder ? `, ${folder.path}` : ""}`}
        after={
          waiting && agent.threadId ? (
            <p className="text-attention-foreground mt-1.5 text-[12px]">
              Waiting for a new thread.{" "}
              <button type="button" onClick={onNewThread} className="underline underline-offset-2">
                Start one now
              </button>
            </p>
          ) : undefined
        }
      >
        <Select value={a.workspace} onValueChange={(v) => void saveWhere({ workspace: v })}>
          <SelectTrigger aria-label="Workspace" className="h-11 w-full md:h-9">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="local">Main checkout</SelectItem>
            <SelectItem value="managed">New worktree</SelectItem>
          </SelectContent>
        </Select>
      </Row>
      <Row title="Compact at" hint={a.compactAt ? `${fmtK(a.compactAt)} tokens.` : "Harness default."}>
        <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => onOpenTab("compact")}>
          Change in Compaction
        </Button>
      </Row>
      <Row title="First message" hint="Offered whenever you start a new thread for this agent.">
        <KickoffInput kickoff={agent.kickoff ?? ""} onSave={(kickoff) => void save({ kickoff })} />
      </Row>
      <Row title="Archive agent" hint="Stops its thread and hides it from the sidebar. Revisions and logs are kept." last>
        <Button variant="outline" size="sm" className="text-destructive hover:text-destructive h-11 md:h-8" onClick={onArchive}>
          Archive
        </Button>
      </Row>
    </div>
  );
}

function Row({
  title,
  hint,
  after,
  last,
  children,
}: {
  title: string;
  hint: string;
  after?: ReactNode;
  last?: boolean;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex flex-col gap-2 py-3.5 md:flex-row md:gap-4", !last && "border-b")}>
      <div className="min-w-0 flex-1">
        <b className="block text-[14px] font-medium">{title}</b>
        <span className="text-muted-foreground mt-0.5 block text-[12px] leading-relaxed">{hint}</span>
      </div>
      <div className="w-full min-w-0 md:w-[260px] md:shrink-0">
        {children}
        {after}
      </div>
    </div>
  );
}

/** Saved on leaving the box or Enter. Empty is not a name, so it goes back to the saved one. */
function NameInput({ name, onSave }: { name: string; onSave: (name: string) => void }) {
  const [text, setText] = useState(name);
  const [was, setWas] = useState(name);
  if (was !== name) {
    setWas(name);
    setText(name);
  }
  const commit = () => {
    const next = text.trim();
    if (!next) setText(name);
    else if (next !== name) onSave(next);
  };
  return (
    <Input
      aria-label="Name"
      value={text}
      autoComplete="off"
      autoCapitalize="off"
      spellCheck={false}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          commit();
        }
      }}
      className="h-11 md:h-9"
    />
  );
}

/** Saved on leaving the box, since a message has newlines in it. */
function KickoffInput({ kickoff, onSave }: { kickoff: string; onSave: (kickoff: string) => void }) {
  const [text, setText] = useState(kickoff);
  const [was, setWas] = useState(kickoff);
  if (was !== kickoff) {
    setWas(kickoff);
    setText(kickoff);
  }
  return (
    <Textarea
      aria-label="First message"
      value={text}
      rows={3}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => text !== kickoff && onSave(text)}
    />
  );
}
