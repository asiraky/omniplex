import { DownloadIcon, PlusIcon, RefreshCwIcon, SquarePenIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import type { InstallScope, Skill } from "~/lib/skills";

import { CommitBar } from "./CommitBar";
import { InstallDialog } from "./InstallDialog";
import type { SkillsContext, SkillsSlots } from "./parts";
import { UpdateDialog } from "./UpdateDialog";

const menuItemClass = "min-h-11 gap-2 text-[13px] md:min-h-0";

/**
 * The toolbar's one way in to a new skill, fetched or written. One button
 * with two choices, because two buttons beside the view switch do not fit a
 * phone's width.
 */
function AddSkill({ ctx }: { ctx: SkillsContext }) {
  const [installing, setInstalling] = useState(false);
  // Bumped on every opening, so each dialog starts at the paste box.
  const [seq, setSeq] = useState(0);

  const installed = (skills: Skill[], scope: InstallScope) => {
    // Folding the answer in is the refresh: it is the placed skills exactly,
    // and it spares a phone the whole list again. It also replaces the list,
    // which is what makes the commit bar look at git.
    ctx.upsert(skills);
    // A first install creates the library, which changes the setup as well.
    if (scope === "user" && !ctx.setup?.exists) ctx.refresh();
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm" className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]">
            <PlusIcon className="size-3.5" />
            Add skill
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-56">
          <DropdownMenuItem
            className={menuItemClass}
            onSelect={() => {
              setSeq((n) => n + 1);
              setInstalling(true);
            }}
          >
            <DownloadIcon className="size-3.5" />
            Install from a repo or folder
          </DropdownMenuItem>
          <DropdownMenuItem className={menuItemClass} onSelect={ctx.newSkill}>
            <SquarePenIcon className="size-3.5" />
            Write a new skill
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <InstallDialog
        key={seq}
        open={installing}
        onOpenChange={setInstalling}
        command={ctx.command}
        scopeArgs={ctx.scopeArgs}
        setup={ctx.setup}
        projectRoot={ctx.list?.projectRoot}
        projectAvailable={ctx.projectAvailable}
        onInstalled={installed}
      />
    </>
  );
}

/**
 * "Check for updates" for one source, reached from its group header or from
 * one of its skills. The dialog is mounted on the first press, not before: a
 * page of groups should not carry a dialog each.
 */
function CheckForUpdates({
  ctx,
  dir,
  repo,
  placement,
}: {
  ctx: SkillsContext;
  dir: string;
  repo: string;
  placement: "group" | "detail";
}) {
  const [open, setOpen] = useState(false);
  const [seq, setSeq] = useState(0);
  const start = () => {
    setSeq((n) => n + 1);
    setOpen(true);
  };
  return (
    <>
      {placement === "group" ? (
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground h-11 shrink-0 text-[12px] md:h-8"
          onClick={start}
        >
          Check for updates
        </Button>
      ) : (
        // The short word keeps Use, Edit, this and Remove on one row at a
        // phone's width; the full name is still what the control is called.
        <Button
          variant="outline"
          size="sm"
          className="h-11 text-[13px] md:h-8 md:text-[12px]"
          aria-label="Check for updates"
          onClick={start}
        >
          <RefreshCwIcon className="size-3.5" />
          Updates
        </Button>
      )}
      {seq > 0 && (
        <UpdateDialog
          key={seq}
          open={open}
          onOpenChange={setOpen}
          command={ctx.command}
          scopeArgs={ctx.scopeArgs}
          dir={dir}
          repo={repo}
          onUpdated={ctx.upsert}
        />
      )}
    </>
  );
}

const install: SkillsSlots["install"] = (ctx) => <AddSkill ctx={ctx} />;

const groupAction: SkillsSlots["groupAction"] = (group, ctx) => {
  const first = group.entries[0]?.copies[0];
  if (!first || !group.repo) return null;
  return <CheckForUpdates ctx={ctx} dir={first.dir} repo={group.repo} placement="group" />;
};

const update: SkillsSlots["update"] = (skill, ctx) =>
  skill.source ? <CheckForUpdates ctx={ctx} dir={skill.dir} repo={skill.source.repo} placement="detail" /> : null;

/** The Skills page: everything. */
export const pageSlots: SkillsSlots = {
  install,
  commitBar: (ctx) => <CommitBar ctx={ctx} />,
  groupAction,
  update,
};

/**
 * The panel tab: installing and updating, without the commit bar. The panel
 * is a phone's width with a thread's worth of other things in it, and the
 * commit is a job for the page.
 */
export const panelSlots: SkillsSlots = { install, groupAction, update };
