import {
  BellIcon,
  ChevronRightIcon,
  FolderIcon,
  KeyRoundIcon,
  PlusIcon,
  SearchIcon,
  SlidersHorizontalIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode, type Ref } from "react";

import type { AuthWires } from "~/components/AuthFlowDialog";
import { GeneralSettings } from "~/components/GeneralSettings";
import { NotificationsSettings } from "~/components/NotificationsSettings";
import { ProjectSettings, type AddFolderRequest } from "~/components/ProjectSettings";
import ProvidersSettings from "~/components/ProvidersSettings";
import { SettingsPane } from "~/components/SettingsPane";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { useCanHover, useIsDesktop } from "~/useMediaQuery";
import {
  entryKey,
  searchSettings,
  settingsIndex,
  type AccountRef,
  type SettingEntry,
} from "~/lib/settingsSearch";
import { cn } from "~/lib/utils";
import type {
  Folder,
  GitHubRepo,
  HarnessMeta,
  Project,
  ProjectDefaults,
  UserConfig,
} from "~/protocol";

export type SettingsSection =
  | { kind: "general" }
  /** addFor opens on adding an account: a harness id preselects it, null asks which. */
  | { kind: "providers"; addFor?: string | null }
  | { kind: "notifications" }
  | { kind: "project"; id: string };

const GENERAL: SettingsSection = { kind: "general" };

const same = (a: SettingsSection | null, b: SettingsSection) =>
  !!a && a.kind === b.kind && (a.kind !== "project" || (b.kind === "project" && a.id === b.id));

/** What a picked result is still waiting to land on; `n` tells two picks of one setting apart. */
type Jump = { anchor: string; n: number };

/**
 * Where focus may go on a pick: the setting's own field, switch or select, or
 * the setting itself when it is a button. Never a button inside one, since
 * that is often a Remove that acts on the next Enter without asking.
 */
const FOCUSABLE =
  "input:not([disabled]), textarea:not([disabled]), [role=combobox], [role=switch]";

/**
 * Brings a setting into view inside `root` and marks it, so the eye finds it.
 * Focus moves to its control only with a mouse: on a phone that would open the
 * keyboard over the very thing it was asked to show. False while the section
 * has not rendered it yet.
 */
function land(root: HTMLElement, anchor: string, focus: boolean): boolean {
  const el = [...root.querySelectorAll<HTMLElement>("[data-setting]")].find(
    (e) => e.dataset.setting === anchor,
  );
  if (!el) return false;
  const reducedMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  el.scrollIntoView?.({ block: "center", behavior: reducedMotion ? "auto" : "smooth" });
  if (focus) {
    const control =
      el.matches(FOCUSABLE) || el instanceof HTMLButtonElement
        ? el
        : el.querySelector<HTMLElement>(FOCUSABLE);
    control?.focus({ preventScroll: true });
  }
  // Off and on again, so a second pick of the same setting plays it again.
  el.removeAttribute("data-found");
  void el.offsetWidth;
  el.setAttribute("data-found", "");
  return true;
}

/**
 * Every setting in one place: yours, the accounts the agents sign in with, and
 * each project's. A list of sections beside the open one on a desktop; on a
 * phone the list first, then the section over it.
 */
export function SettingsScreen({
  at,
  projects,
  harnesses,
  userConfig,
  threadCounts,
  onSaveUserConfig,
  hasElevenLabsKey,
  onSetElevenLabsKey,
  providers,
  project: projectActions,
  onAddProject,
  onClose,
}: {
  /** The section to open on. Absent opens the list on a phone, General on a desktop. */
  at?: SettingsSection;
  projects: Project[];
  harnesses: HarnessMeta[];
  /** Null until the server has answered; General waits for it. */
  userConfig: UserConfig | null;
  threadCounts: Record<string, number>;
  onSaveUserConfig: (cfg: UserConfig) => Promise<void>;
  hasElevenLabsKey: boolean;
  onSetElevenLabsKey: (key: string) => Promise<void>;
  providers: {
    wires: AuthWires;
    onOpenTerminal: (instanceId: string) => void;
    onRecheck: () => Promise<void> | void;
  };
  project: {
    onSave: (
      id: string,
      name: string,
      defaults: ProjectDefaults,
      folders: Folder[],
    ) => Promise<void>;
    onAddFolder: (projectId: string, req: AddFolderRequest) => Promise<Project>;
    onRemoveFolder: (projectId: string, folderId: string) => Promise<Project>;
    listRepos: () => Promise<GitHubRepo[]>;
    onDelete: (id: string) => Promise<void>;
  };
  onAddProject: () => void;
  onClose: () => void;
}) {
  const isDesktop = useIsDesktop();
  const canHover = useCanHover();
  const [picked, setPicked] = useState<SettingsSection | null>(at ?? null);
  const [query, setQuery] = useState("");
  const [jump, setJump] = useState<Jump | null>(null);
  // Which Providers view a pick asked for: the list, or one account.
  const [providersShow, setProvidersShow] = useState<{ account?: AccountRef; n: number }>();
  const contentRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const picks = useRef(0);
  const landedPick = useRef(0);

  const index = useMemo(
    () => settingsIndex({ projects, harnesses, hasElevenLabsKey }),
    [projects, harnesses, hasElevenLabsKey],
  );
  const results = useMemo(() => searchSettings(index, query), [index, query]);
  const searching = query.trim() !== "";
  // A project deleted from under its own section leaves nothing to show.
  const valid =
    picked?.kind === "project" && !projects.some((p) => p.id === picked.id) ? null : picked;
  const section = valid ?? (isDesktop ? GENERAL : null);
  // Moving by hand drops a pick that has not landed yet, so it cannot land
  // later on a same-named setting somewhere else, and the account a pick
  // opened, so Providers opens on its list next time.
  const goTo = (s: SettingsSection | null) => {
    setJump(null);
    setProvidersShow(undefined);
    setPicked(s);
  };
  const back = isDesktop ? undefined : () => goTo(null);

  const pick = (e: SettingEntry) => {
    const n = ++picks.current;
    if (e.place.kind === "providers") {
      setPicked({ kind: "providers" });
      setProvidersShow({ account: e.place.account, n });
    } else {
      setPicked(e.place);
    }
    setJump({ anchor: e.anchor, n });
  };

  const clearSearch = () => {
    setQuery("");
    searchRef.current?.focus();
  };

  // Once the picked section shows the setting. General waits for the config,
  // so this tries again when that arrives rather than giving up.
  useEffect(() => {
    if (!jump || landedPick.current === jump.n || !contentRef.current) return;
    if (land(contentRef.current, jump.anchor, canHover)) landedPick.current = jump.n;
  }, [jump, section, userConfig, canHover]);

  let pane: ReactNode = null;
  if (section?.kind === "general") {
    pane = userConfig ? (
      <GeneralSettings
        userConfig={userConfig}
        harnesses={harnesses}
        onSave={onSaveUserConfig}
        hasElevenLabsKey={hasElevenLabsKey}
        onSetElevenLabsKey={onSetElevenLabsKey}
        onBack={back}
      />
    ) : (
      <SettingsPane title="General" onBack={back}>
        <p className="text-muted-foreground text-[13px]">Loading…</p>
      </SettingsPane>
    );
  } else if (section?.kind === "providers") {
    pane = (
      <ProvidersSettings
        harnesses={harnesses}
        {...providers}
        addFor={section.addFor}
        show={providersShow}
        onBack={back}
      />
    );
  } else if (section?.kind === "notifications") {
    pane = <NotificationsSettings onBack={back} />;
  } else if (section?.kind === "project") {
    const project = projects.find((p) => p.id === section.id)!;
    pane = (
      <ProjectSettings
        // A different project is a different form, not this one's edits.
        key={project.id}
        project={project}
        harnesses={harnesses}
        {...projectActions}
        threadCount={threadCounts[project.id] ?? 0}
        hasElevenLabsKey={hasElevenLabsKey}
        defaultVoice={userConfig?.voice ?? ""}
        onDeleted={() => setPicked(null)}
        onBack={back}
      />
    );
  }

  const item = (target: SettingsSection, icon: ReactNode, label: string) => (
    <NavItem
      key={target.kind === "project" ? target.id : target.kind}
      icon={icon}
      label={label}
      current={isDesktop && same(section, target)}
      onClick={() => goTo(target)}
    />
  );

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        ref={contentRef}
        fullscreenOnMobile
        className="flex gap-0 p-0 md:h-[min(90dvh,44rem)] md:max-w-3xl"
        // Radix puts focus on the first control, the search box. Typing there
        // is the point with a keyboard; on a phone it would open the keyboard
        // on arrival, so focus lands on the dialog itself.
        onOpenAutoFocus={(e) => {
          if (canHover) return;
          e.preventDefault();
          (e.currentTarget as HTMLElement | null)?.focus();
        }}
        // Escape in a search with words in it clears them; the next one closes.
        onEscapeKeyDown={(e) => {
          if (searching && document.activeElement === searchRef.current) {
            e.preventDefault();
            setQuery("");
          }
        }}
      >
        <DialogTitle className="sr-only">Settings</DialogTitle>
        <DialogDescription className="sr-only">
          Your defaults, the accounts agents sign in with, notifications, and each project's settings.
        </DialogDescription>

        {(isDesktop || !section) && (
          <nav
            aria-label="Settings sections"
            className={cn(
              "scroll-thin flex min-h-0 flex-col overflow-y-auto",
              isDesktop
                ? "bg-muted/40 w-56 shrink-0 rounded-l-lg border-r p-2"
                : "w-full px-3 pt-[calc(0.75rem+env(safe-area-inset-top))] pb-3",
            )}
          >
            <p
              aria-hidden
              className="px-2.5 pt-2 pb-3 text-lg font-semibold md:pt-1.5 md:pb-2 md:text-base"
            >
              Settings
            </p>
            <SearchBox
              ref={searchRef}
              value={query}
              onChange={setQuery}
              onSubmit={() => results[0] && pick(results[0])}
              onClear={clearSearch}
            />
            {searching ? (
              <SearchResults results={results} onPick={pick} />
            ) : (
              <>
                <div className="flex flex-col gap-0.5">
                  {item(GENERAL, <SlidersHorizontalIcon />, "General")}
                  {item({ kind: "providers" }, <KeyRoundIcon />, "Providers")}
                  {item({ kind: "notifications" }, <BellIcon />, "Notifications")}
                </div>
                <p className="text-muted-foreground px-2.5 pt-4 pb-1 text-[11px] font-medium">
                  Projects
                </p>
                <div className="flex flex-col gap-0.5">
                  {projects.map((p) => item({ kind: "project", id: p.id }, <FolderIcon />, p.name))}
                  <NavItem icon={<PlusIcon />} label="New project" onClick={onAddProject} muted />
                </div>
              </>
            )}
          </nav>
        )}

        {pane}
      </DialogContent>
    </Dialog>
  );
}

function NavItem({
  icon,
  label,
  current,
  muted,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  current?: boolean;
  muted?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={current ? "page" : undefined}
      className={cn(
        "hover:bg-accent focus-visible:ring-ring flex min-h-11 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-[14px] outline-none focus-visible:ring-2 md:min-h-8 md:text-[13px] [&_svg]:size-4 [&_svg]:shrink-0",
        current && "bg-accent text-accent-foreground font-medium",
        muted && "text-muted-foreground",
      )}
    >
      <span className="text-muted-foreground flex">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {!muted && <ChevronRightIcon className="text-muted-foreground md:hidden" />}
    </button>
  );
}

function SearchBox({
  ref,
  value,
  onChange,
  onSubmit,
  onClear,
}: {
  ref: Ref<HTMLInputElement>;
  value: string;
  onChange: (v: string) => void;
  /** Enter: open the first result. */
  onSubmit: () => void;
  /** The × button, which leaves focus in the box for the next search. */
  onClear: () => void;
}) {
  return (
    <div className="relative mb-2">
      <SearchIcon
        aria-hidden
        className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
      />
      <Input
        ref={ref}
        type="search"
        aria-label="Search settings"
        placeholder="Search settings"
        autoComplete="off"
        spellCheck={false}
        enterKeyHint="go"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.nativeEvent.isComposing) {
            e.preventDefault();
            onSubmit();
          }
        }}
        className="bg-background dark:bg-background pr-9 pl-8 md:h-8 md:text-[13px] [&::-webkit-search-cancel-button]:hidden"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear search"
          onClick={onClear}
          className="text-muted-foreground hover:text-foreground absolute top-1/2 right-0 flex size-11 -translate-y-1/2 items-center justify-center md:size-8 [&_svg]:size-3.5"
        >
          <XIcon />
        </button>
      )}
    </div>
  );
}

/** The matching settings, each with the section it is in. */
function SearchResults({
  results,
  onPick,
}: {
  results: SettingEntry[];
  onPick: (e: SettingEntry) => void;
}) {
  if (results.length === 0) {
    return <p className="text-muted-foreground px-2.5 py-2 text-[13px]">No settings match.</p>;
  }
  return (
    <ul aria-label="Matching settings" className="flex flex-col gap-0.5">
      {results.map((e) => (
        <li key={entryKey(e)}>
          <button
            type="button"
            onClick={() => onPick(e)}
            className="hover:bg-accent focus-visible:ring-ring flex min-h-11 w-full flex-col justify-center rounded-md px-2.5 py-1.5 text-left outline-none focus-visible:ring-2 md:min-h-8"
          >
            <span className="w-full truncate text-[14px] md:text-[13px]">{e.label}</span>
            <span className="text-muted-foreground w-full truncate text-[12px] md:text-[11px]">
              {e.where}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}
