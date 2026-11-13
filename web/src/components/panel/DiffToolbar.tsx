import {
  ChevronDownIcon,
  ChevronUpIcon,
  Columns2Icon,
  FlaskConicalOffIcon,
  PilcrowIcon,
  SearchIcon,
  SlidersHorizontalIcon,
  UnfoldVerticalIcon,
  WrapTextIcon,
  XIcon,
} from "lucide-react";
import { useRef, type ComponentType, type KeyboardEvent, type RefObject } from "react";

import { IconButton } from "~/components/IconButton";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Input } from "~/components/ui/input";
import { cn } from "~/lib/utils";

/** One on/off view option, and what to call it. */
export interface DiffToggle {
  label: string;
  Icon: ComponentType<{ className?: string }>;
  on: boolean;
  set: (on: boolean) => void;
}

export const TOGGLE_ICONS = {
  split: Columns2Icon,
  wrap: WrapTextIcon,
  whitespace: PilcrowIcon,
  context: UnfoldVerticalIcon,
  tests: FlaskConicalOffIcon,
};

const PRESSED = "aria-pressed:bg-accent aria-pressed:text-foreground text-muted-foreground";

/**
 * The view options. On a desktop, a row of icon toggles that wraps under the
 * scope when the panel is docked narrow; on a phone, one menu, since a row of
 * 44px targets would not fit beside the scope at all.
 */
export function DiffToolbar({
  desktop,
  toggles,
  findOpen,
  onFind,
  menuFocus,
}: {
  desktop: boolean;
  toggles: DiffToggle[];
  findOpen: boolean;
  /** Open (or, from the desktop toggle, close) the find bar. */
  onFind: (open: boolean) => void;
  /** Where focus goes when the phone menu closes on "Find in diff". */
  menuFocus: RefObject<HTMLInputElement | null>;
}) {
  // Set by the find item, read as the menu closes in its wake.
  const finding = useRef(false);
  if (desktop) {
    return (
      <div role="toolbar" aria-label="Diff options" className="flex shrink-0 items-center">
        {toggles.map((t) => (
          <IconButton key={t.label} label={t.label} aria-pressed={t.on} className={PRESSED} onClick={() => t.set(!t.on)}>
            <t.Icon />
          </IconButton>
        ))}
        <IconButton label="Find in diff" aria-pressed={findOpen} className={PRESSED} onClick={() => onFind(!findOpen)}>
          <SearchIcon />
        </IconButton>
      </div>
    );
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton label="Diff options">
          <SlidersHorizontalIcon />
        </IconButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        onCloseAutoFocus={(e) => {
          // The menu hands focus back to its trigger as it closes, which would
          // take it straight out of the find field it just opened.
          if (!finding.current) return;
          finding.current = false;
          e.preventDefault();
          menuFocus.current?.focus();
        }}
      >
        {toggles.map((t) => (
          <DropdownMenuCheckboxItem
            key={t.label}
            checked={t.on}
            onCheckedChange={(v) => t.set(v === true)}
            // Several are often flipped at once; the menu stays open for it.
            onSelect={(e) => e.preventDefault()}
            className="min-h-11"
          >
            {t.label}
          </DropdownMenuCheckboxItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="min-h-11"
          onSelect={() => {
            finding.current = true;
            onFind(true);
          }}
        >
          <SearchIcon /> Find in diff
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Find in diff: a query, how far through its matches you are, and the way to
 * step through them. Enter goes to the next file, Shift+Enter the previous,
 * Escape closes.
 */
export function DiffFindBar({
  inputRef,
  query,
  onQuery,
  current,
  total,
  searching,
  onStep,
  onClose,
}: {
  inputRef: RefObject<HTMLInputElement | null>;
  query: string;
  onQuery: (query: string) => void;
  /** The 0-based match on screen, or -1 before the first step. */
  current: number;
  /** Null until the query is long enough to look for. */
  total: number | null;
  searching: boolean;
  onStep: (by: 1 | -1) => void;
  onClose: () => void;
}) {
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      onStep(e.shiftKey ? -1 : 1);
    } else if (e.key === "Escape") {
      // The docked panel closes on an Escape nothing else claimed.
      e.preventDefault();
      e.stopPropagation();
      onClose();
    }
  };
  return (
    <div className="flex items-center gap-1 border-b px-3 py-1">
      <Input
        ref={inputRef}
        type="search"
        aria-label="Find in diff"
        placeholder="Find in diff"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        onKeyDown={onKeyDown}
        enterKeyHint="search"
        className="h-11 min-w-0 flex-1 text-[13px] md:h-8"
      />
      <span
        aria-live="polite"
        className={cn("text-muted-foreground w-12 shrink-0 text-center text-[11px] tabular-nums", searching && "opacity-60")}
      >
        {total === null ? "" : total === 0 ? (searching ? "…" : "0/0") : `${current < 0 ? 0 : current + 1}/${total}`}
      </span>
      <IconButton label="Previous match" disabled={!total} onClick={() => onStep(-1)}>
        <ChevronUpIcon />
      </IconButton>
      <IconButton label="Next match" disabled={!total} onClick={() => onStep(1)}>
        <ChevronDownIcon />
      </IconButton>
      <IconButton label="Close find" onClick={onClose}>
        <XIcon />
      </IconButton>
    </div>
  );
}
