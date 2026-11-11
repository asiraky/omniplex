import { ChevronLeftIcon } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";

import type { DraftPreset } from "~/app/useThreadSelection";
import { folderName } from "~/components/threadDraft/shared";
import { Command, CommandInput, CommandList } from "~/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "~/components/ui/dialog";
import { Kbd } from "~/components/ui/kbd";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import type { CommandId } from "~/lib/keybindings";
import { cn } from "~/lib/utils";
import type { Project, ThreadMeta, Workspace } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import { useShowShortcuts } from "~/useShortcuts";

import {
  currentStep,
  orderProjects,
  pickFolder,
  pickProject,
  stepBack,
  type Next,
  type NewThreadPicks,
} from "./palette/newThread";
import { NewThreadPage, STEP_PROMPT } from "./palette/NewThreadPage";
import { PaletteTouch } from "./palette/PaletteRow";
import { RenamePage } from "./palette/RenamePage";
import { RootPage } from "./palette/RootPage";
import { useWorkspaces } from "./palette/useWorkspaces";

export type PalettePage = "root" | "newThread";

export interface PaletteAction {
  id: string;
  label: string;
  group: "Thread" | "App";
  /** Shows its chord on the row. */
  command?: CommandId;
  keywords?: string[];
  run: () => void;
}

export interface CommandPaletteProps {
  open: boolean;
  /** The page shown each time it opens. */
  initialPage: PalettePage;
  onOpenChange: (open: boolean) => void;
  threads: ThreadMeta[];
  activeId: string | null;
  projects: Project[];
  /** Listed, and so highlighted, first in the project step. */
  defaultProjectId?: string;
  /** Built by the app; the palette only renders and filters them. */
  actions: PaletteAction[];
  onSelectThread: (id: string) => void;
  /** Always called with `focus: true`. */
  onNewThread: (preset: DraftPreset) => void;
  onListWorkspaces: (projectId: string, folderId: string) => Promise<Workspace[]>;
  /** Renames the active thread. Absent hides "Rename thread…". */
  onRename?: (title: string) => void;
}

type View = { page: "root" } | { page: "rename" } | ({ page: "newThread" } & NewThreadPicks);

const startAt = (page: PalettePage): View => ({ page });

/**
 * ⌘K: one search over threads and actions, and the keyboard's way to start a
 * thread in a given project, folder and worktree. A centred dialog on a
 * desktop; on a phone, a full-height sheet with touch-sized rows and no key
 * hints, where it is mostly thread search.
 *
 * It closes itself after it runs an action, opens a thread or starts one, and
 * then does not hand focus back to whatever had it before: what it just did
 * decides where focus goes — the new draft's composer focuses itself on mount,
 * and restoring the old focus afterwards would take it away again.
 */
export function CommandPalette({
  open,
  initialPage,
  onOpenChange,
  threads,
  activeId,
  projects,
  defaultProjectId,
  actions,
  onSelectThread,
  onNewThread,
  onListWorkspaces,
  onRename,
}: CommandPaletteProps) {
  const isDesktop = useIsDesktop();
  const hints = useShowShortcuts();
  const touch = !isDesktop;

  const [view, setView] = useState<View>(() => startAt(initialPage));
  const [query, setQuery] = useState("");
  // Every opening starts over on the page asked for, with nothing typed.
  // Adjusted during render so it never paints the last visit's page first.
  const [seenOpen, setSeenOpen] = useState(open);
  if (seenOpen !== open) {
    setSeenOpen(open);
    if (open) {
      setView(startAt(initialPage));
      setQuery("");
    }
  }
  // Set when the palette closes because it did something; see above.
  const handedOff = useRef(false);

  const step = view.page === "newThread" ? currentStep(projects, view) : null;
  const listing = useWorkspaces(
    onListWorkspaces,
    step?.kind === "workspace" ? { projectId: step.project.id, folderId: step.folder.id } : null,
  );
  const active = threads.find((t) => t.id === activeId);
  const canRename = !!onRename && !!active;

  const go = (next: View) => {
    setView(next);
    setQuery("");
  };
  const close = () => {
    handedOff.current = true;
    onOpenChange(false);
  };
  const back = () => {
    if (view.page === "rename") go({ page: "root" });
    if (step) {
      const picks = stepBack(step);
      go(picks ? { page: "newThread", ...picks } : { page: "root" });
    }
  };
  const advance = (next: Next) => {
    if ("picks" in next) {
      go({ page: "newThread", ...next.picks });
      return;
    }
    onNewThread({ ...next.done, focus: true });
    close();
  };

  const crumbs: string[] = [];
  if (step) {
    crumbs.push("New thread in…");
    if (step.kind !== "project") crumbs.push(step.project.name);
    if (step.kind === "workspace") crumbs.push(folderName(step.folder.path));
  }
  if (view.page === "rename") crumbs.push("Rename thread");

  const header = crumbs.length > 0 && (
    <div
      className={cn(
        "text-muted-foreground flex items-center gap-1 border-b px-1.5 text-xs",
        touch ? "h-12 pr-12" : "h-9",
      )}
    >
      {/* The pointer's way back; the keyboard's is Backspace in an empty field. */}
      <button
        type="button"
        aria-label="Back"
        onClick={back}
        className={cn(
          "hover:text-foreground flex shrink-0 items-center justify-center rounded-md",
          touch ? "size-11" : "size-7",
        )}
      >
        <ChevronLeftIcon className="size-4" />
      </button>
      <span className="min-w-0 truncate">{crumbs.join(" › ")}</span>
    </div>
  );

  const list =
    view.page === "rename" && active ? (
      <RenamePage
        title={active.title}
        touch={touch}
        onBack={back}
        onSave={(title) => {
          if (title !== null) onRename?.(title);
          close();
        }}
      />
    ) : (
      <Command
        // A fresh list per step. cmdk highlights the first row of a list it
        // mounts, but keeps a highlight whose row went away when the step
        // changes under it, and Enter on nothing would stall the flow.
        key={step ? `newThread:${step.kind}` : view.page}
        shouldFilter={false}
        loop
        className="min-h-0 flex-1 rounded-none bg-transparent"
      >
        <CommandInput
          autoFocus
          value={query}
          onValueChange={setQuery}
          placeholder={step ? STEP_PROMPT[step.kind] : "Search threads and actions…"}
          onKeyDown={(e) => {
            if (e.key === "Backspace" && query === "" && view.page !== "root") {
              e.preventDefault();
              back();
            }
          }}
          wrapperClassName={touch ? "h-12 pr-12" : "h-11"}
          // 16px on touch screens, or iOS zooms the page on focus.
          className={touch ? "text-[16px]" : undefined}
        />
        <CommandList
          className={cn("scroll-thin", touch ? "max-h-none flex-1 overscroll-contain" : "max-h-[min(60dvh,24rem)]")}
        >
          {step ? (
            <NewThreadPage
              step={step}
              query={query}
              projects={orderProjects(projects, defaultProjectId)}
              listing={listing}
              onPickProject={(p) => advance(pickProject(p))}
              onPickFolder={(folderId) => step.kind === "folder" && advance(pickFolder(step.project, folderId))}
              onPickWorkspace={(choice) =>
                step.kind === "workspace" &&
                advance({ done: { projectId: step.project.id, folderId: step.folder.id, ...choice } })
              }
            />
          ) : (
            <RootPage
              query={query}
              threads={threads}
              activeId={activeId}
              projects={projects}
              actions={actions}
              onSelectThread={(id) => {
                onSelectThread(id);
                close();
              }}
              onNewThreadIn={() => go({ page: "newThread" })}
              onRename={canRename ? () => go({ page: "rename" }) : undefined}
              onAction={(a) => {
                // Closed first, so an action that opens a dialog of its own
                // opens it over nothing.
                close();
                a.run();
              }}
            />
          )}
        </CommandList>
      </Command>
    );

  const footer = hints && view.page !== "rename" && (
    <div className="text-muted-foreground flex items-center gap-3 border-t px-3 py-2 text-[11px]">
      <Hint keys={["↑", "↓"]}>navigate</Hint>
      <Hint keys={["↵"]}>select</Hint>
      {view.page !== "root" && <Hint keys={["⌫"]}>back</Hint>}
      <Hint keys={["Esc"]}>close</Hint>
    </div>
  );

  const body = (
    <PaletteTouch.Provider value={touch}>
      {header}
      {list}
      {footer}
    </PaletteTouch.Provider>
  );

  const contentProps = {
    "aria-describedby": undefined,
    onEscapeKeyDown: (e: KeyboardEvent) => {
      // Escape out of a rename is "never mind", not "close everything".
      if (view.page === "rename") {
        e.preventDefault();
        back();
      }
    },
    onCloseAutoFocus: (e: Event) => {
      if (handedOff.current) e.preventDefault();
      handedOff.current = false;
    },
  };

  if (touch) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          {...contentProps}
          className="h-[100dvh] gap-0 p-0 pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] [&>[data-slot=sheet-close]]:top-[calc(0.125rem+env(safe-area-inset-top))]"
        >
          <SheetTitle className="sr-only">Command palette</SheetTitle>
          {body}
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        {...contentProps}
        showCloseButton={false}
        className="top-[15%] flex translate-y-0 flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
      >
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        {body}
      </DialogContent>
    </Dialog>
  );
}

function Hint({ keys, children }: { keys: string[]; children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1">
      {keys.map((k) => (
        <Kbd key={k}>{k}</Kbd>
      ))}
      {children}
    </span>
  );
}
