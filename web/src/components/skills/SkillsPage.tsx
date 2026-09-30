import { XIcon } from "lucide-react";

import { IconButton } from "~/components/IconButton";
import type { Skill, SkillsScope } from "~/lib/skills";

import { SkillsSurface, type SkillsCommand, type SkillsSlots } from "./SkillsSurface";

/** Which skills the page is showing, in a line: the answer changes with where it was opened from. */
function scopeLine(scope: SkillsScope): string {
  if (scope.kind === "personal") return "Your personal skills. Open a project to see its skills too.";
  const project = scope.projectName ? `the ${scope.projectName} project` : "the project";
  return scope.kind === "thread"
    ? `Skills the current thread can use: yours and those of ${project}.`
    : `Your skills and those of ${project}.`;
}

/**
 * The Skills page: the same surface the panel tab shows, given the whole
 * viewport. Reachable without a thread, so it says which skills it is looking
 * at rather than leaving that to where the reader came from.
 */
export function SkillsPage({
  command,
  scope,
  onUse,
  onClose,
  slots,
}: {
  command: SkillsCommand;
  scope: SkillsScope;
  /** Given only when there is a thread whose composer can take the skill. */
  onUse?: (skill: Skill) => void | Promise<void>;
  onClose: () => void;
  slots?: SkillsSlots;
}) {
  return (
    <div className="bg-background fixed inset-0 z-50 flex flex-col pb-[env(safe-area-inset-bottom)]">
      <header className="flex items-start gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-4">
        <IconButton label="Close skills" onClick={onClose}>
          <XIcon />
        </IconButton>
        <div className="min-w-0 flex-1 py-1">
          <h1 className="text-[15px] leading-tight font-semibold">Skills</h1>
          <p className="text-muted-foreground mt-0.5 text-[12px] leading-snug">{scopeLine(scope)}</p>
        </div>
      </header>
      <div className="min-h-0 flex-1 border-t">
        {/* A list of names and sentences: past this width the lines only get
            harder to follow. */}
        <div className="mx-auto h-full w-full max-w-3xl">
          <SkillsSurface
            command={command}
            threadId={scope.threadId}
            projectId={scope.threadId ? undefined : scope.projectId}
            onUse={onUse}
            slots={slots}
          />
        </div>
      </div>
    </div>
  );
}
