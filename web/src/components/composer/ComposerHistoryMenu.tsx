import { BookmarkPlusIcon, HistoryIcon, PaperclipIcon, XIcon } from "lucide-react";
import { useRef, type SyntheticEvent } from "react";

import type { StashEntry } from "~/app/stashStore";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";

/** How many sent prompts the menu offers. ArrowUp reaches the rest. */
export const MENU_RECENT = 8;

/**
 * The composer's one menu for words that are not in it right now: parking the
 * draft, the drafts parked so far, and the prompts already sent in this
 * thread. A phone has no ArrowUp, so this is its way back to an earlier prompt.
 */
export function ComposerHistoryMenu({
  disabled,
  canStash,
  stashed,
  recent,
  onStash,
  onTake,
  onDrop,
  onRecall,
  onPicked,
}: {
  disabled: boolean;
  /** There is text or a ready attachment to park. */
  canStash: boolean;
  /** Newest first. */
  stashed: readonly StashEntry[];
  /** Newest first. */
  recent: readonly string[];
  onStash: () => void;
  onTake: (id: string) => void;
  onDrop: (id: string) => void;
  onRecall: (text: string) => void;
  /** The menu has gone after a pick, so focus can go where the pick wants it. */
  onPicked: () => void;
}) {
  // Whether the menu closed on a pick. Focus is the pick's to place, not the
  // trigger's, and only once the menu is gone: until then the item under the
  // pointer can take focus back, and leaves it on the page as it unmounts.
  const picked = useRef(false);
  const pick = (run: () => void) => () => {
    picked.current = true;
    run();
  };
  return (
    // Not modal: a modal menu holds focus inside itself until it is gone, and
    // a pick has to type into the textarea while it is still closing.
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label="Stash and recent prompts"
          title="Stash and recent prompts"
          className="text-muted-foreground hover:text-foreground size-11 shrink-0 rounded-full md:size-8"
        >
          <HistoryIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="top"
        align="start"
        // Off the screen's edges on a phone, where the menu is near its width.
        collisionPadding={12}
        className="w-80 max-w-[calc(100vw-1.5rem)]"
        onCloseAutoFocus={(e) => {
          if (!picked.current) return;
          picked.current = false;
          e.preventDefault();
          onPicked();
        }}
      >
        <DropdownMenuItem disabled={!canStash} onSelect={pick(onStash)} className={ROW}>
          <BookmarkPlusIcon />
          Stash draft
        </DropdownMenuItem>

        {stashed.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className={LABEL}>Stashed</DropdownMenuLabel>
            {stashed.map((entry) => (
              <DropdownMenuItem
                key={entry.id}
                onSelect={pick(() => onTake(entry.id))}
                // The keyboard's way to the remove button, which arrow keys skip.
                onKeyDown={(e) => {
                  if (e.key !== "Delete" && e.key !== "Backspace") return;
                  e.preventDefault();
                  onDrop(entry.id);
                }}
                className={ROW}
              >
                <span className="min-w-0 flex-1">
                  <Snippet text={entry.text} />
                  {entry.attachments.length > 0 && (
                    <span className="text-muted-foreground mt-0.5 flex items-center gap-1 text-xs">
                      <PaperclipIcon className="size-3" />
                      {entry.attachments.length === 1
                        ? "1 attachment"
                        : `${entry.attachments.length} attachments`}
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  aria-label="Remove stashed draft"
                  title="Remove"
                  // The row picks on pointer up and on click; this button's
                  // press must reach neither.
                  onPointerDown={stop}
                  onPointerUp={stop}
                  onClick={(e) => {
                    stop(e);
                    onDrop(entry.id);
                  }}
                  className="text-muted-foreground hover:text-foreground hover:bg-muted -my-1 -mr-1 flex size-7 shrink-0 items-center justify-center rounded-sm pointer-coarse:size-10"
                >
                  <XIcon className="size-3.5" />
                </button>
              </DropdownMenuItem>
            ))}
          </>
        )}

        <DropdownMenuSeparator />
        <DropdownMenuLabel className={LABEL}>Recent prompts</DropdownMenuLabel>
        {recent.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">Nothing sent in this thread yet</p>
        ) : (
          recent.map((text, i) => (
            <DropdownMenuItem key={i} onSelect={pick(() => onRecall(text))} className={ROW}>
              <Snippet text={text} />
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// A fingertip's height on a touch screen.
const ROW = "items-start pointer-coarse:min-h-11 pointer-coarse:py-2.5";
const LABEL = "text-muted-foreground text-xs font-normal";

function stop(e: SyntheticEvent) {
  e.preventDefault();
  e.stopPropagation();
}

/** A prompt's first two lines, its breaks folded to spaces. */
function Snippet({ text }: { text: string }) {
  const flat = text.trim().replace(/\s+/g, " ");
  return (
    <span className={flat ? "line-clamp-2 break-words" : "text-muted-foreground italic"}>
      {flat || "No text"}
    </span>
  );
}
