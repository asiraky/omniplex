import { GitBranchIcon } from "lucide-react";

import { WorkspacePicker } from "~/components/WorkspacePicker";
import { Button } from "~/components/ui/button";
import { Label } from "~/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { cn } from "~/lib/utils";
import type { Folder, UserConfig } from "~/protocol";
import { Chip } from "./parts";
import type { WorkspaceChoiceState } from "./useWorkspaceChoice";

// The Base dropdown's "use the default base" option. A Radix Select item
// cannot carry an empty value, and the space makes it an impossible branch name.
const BASE_DEFAULT = "default base";

const linkClass = "h-8 px-0 text-[12px]";

/** The main checkout, or a worktree. Continuing an existing worktree is a worktree too. */
function KindChoice({ folder, git }: { folder: Folder; git: WorkspaceChoiceState }) {
  const kinds = [
    {
      id: "main",
      label: "Main checkout",
      hint: git.mainBranch ? `${git.mainBranch} · ${folder.path}` : folder.path,
    },
    {
      id: "branch",
      label: "New worktree",
      hint: "New branch in its own git worktree. The main checkout is untouched.",
    },
  ] as const;
  return (
    <div role="radiogroup" aria-label="Git" className="flex flex-col gap-1.5">
      {kinds.map((k) => {
        const picked = k.id === "main" ? git.kind === "main" : git.kind !== "main";
        return (
          <button
            key={k.id}
            type="button"
            role="radio"
            aria-checked={picked}
            onClick={() => {
              if (!picked) git.pickKind(k.id);
            }}
            className={cn(
              "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2",
              picked ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
            )}
          >
            <span className="text-[13px] leading-tight">{k.label}</span>
            <span className="text-muted-foreground truncate text-[11px] leading-tight">
              {k.hint}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** A new worktree: its branch name and what it branches from, both optional. */
function NewWorktreeFields({
  folder,
  git,
  userConfig,
}: {
  folder: Folder;
  git: WorkspaceChoiceState;
  userConfig: UserConfig | null;
}) {
  return (
    <>
      <div className="flex flex-wrap gap-x-3">
        <Button
          type="button"
          variant="link"
          size="sm"
          aria-expanded={git.naming}
          className={linkClass}
          onClick={() => git.setNaming(!git.naming)}
        >
          {git.naming ? "Let Omniplex name the branch" : "Name the branch"}
        </Button>
        {git.attachable.length > 0 && (
          <Button
            type="button"
            variant="link"
            size="sm"
            className={linkClass}
            onClick={() => git.pickKind("attach")}
          >
            Use an existing worktree
          </Button>
        )}
      </div>

      {git.naming && (
        <>
          <div className="space-y-1.5">
            <Label htmlFor="new-thread-workspace">Branch</Label>
            <WorkspacePicker
              id="new-thread-workspace"
              mode="create"
              value={git.choice}
              onChange={git.setChoice}
              workspaces={git.attachable}
              issues={git.issues.issues}
              issuesError={git.issues.issuesError}
              userConfig={userConfig}
              loading={git.loadingIssues}
              placeholder="issue/482-fix-login"
            />
          </div>
          <div className="space-y-1.5 pt-1">
            <Label htmlFor="new-thread-base">Base</Label>
            <Select
              value={git.baseRef || BASE_DEFAULT}
              onValueChange={(v) => git.setBaseRef(v === BASE_DEFAULT ? "" : v)}
            >
              <SelectTrigger id="new-thread-base" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={BASE_DEFAULT}>
                  Default
                  {folder.baseBranch ? ` (${folder.baseBranch})` : ""}
                </SelectItem>
                {git.baseChoices.map((b) => (
                  <SelectItem key={b} value={b}>
                    {b}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </>
      )}
    </>
  );
}

/** A worktree another thread already made, to carry on its work. */
function ExistingWorktreeField({
  git,
  userConfig,
}: {
  git: WorkspaceChoiceState;
  userConfig: UserConfig | null;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor="new-thread-attach">Worktree</Label>
        <Button
          type="button"
          variant="link"
          size="sm"
          className={linkClass}
          onClick={() => git.pickKind("branch")}
        >
          New worktree instead
        </Button>
      </div>
      <WorkspacePicker
        id="new-thread-attach"
        mode="attach"
        value={git.choice}
        onChange={git.setChoice}
        workspaces={git.attachable}
        issues={git.issues.issues}
        issuesError={git.issues.issuesError}
        userConfig={userConfig}
        loading={git.loadingSpaces}
        placeholder="Search worktrees"
      />
    </div>
  );
}

/** Which checkout of a git repo the thread works in. */
export function GitChip({
  folder,
  git,
  userConfig,
}: {
  folder: Folder;
  git: WorkspaceChoiceState;
  userConfig: UserConfig | null;
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Chip label="Git" icon={<GitBranchIcon />}>
          {git.label}
        </Chip>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(22rem,calc(100vw-2rem))] space-y-2">
        <KindChoice folder={folder} git={git} />
        {git.kind === "branch" && (
          <NewWorktreeFields folder={folder} git={git} userConfig={userConfig} />
        )}
        {git.kind === "attach" && <ExistingWorktreeField git={git} userConfig={userConfig} />}
      </PopoverContent>
    </Popover>
  );
}
