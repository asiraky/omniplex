import type { ReactElement, RefObject } from "react";

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import type { ComposerItem } from "~/protocol";

interface MenuListProps {
  matches: ComposerItem[];
  activeIndex: number;
  loading: boolean;
  onHover: (index: number) => void;
  onChoose: (item: ComposerItem) => void;
}

/** The completion menu, in a popover above the textarea. On a phone it must
    not cover the textarea: the composer sits at the bottom of the screen, so
    anything anchored there hides what is being typed. */
export function CommandMenu({
  open,
  anchor,
  anchorRef,
  onDismiss,
  ...list
}: MenuListProps & {
  open: boolean;
  /** The textarea the menu completes into. */
  anchor: ReactElement;
  anchorRef: RefObject<HTMLElement | null>;
  /** Keeps the menu shut for the current token. */
  onDismiss: () => void;
}) {
  return (
    <Popover open={open}>
      <PopoverAnchor asChild>{anchor}</PopoverAnchor>
      <PopoverContent
        side="top"
        align="start"
        // Flipping below would cover the textarea; the list shrinks to fit instead.
        avoidCollisions={false}
        onOpenAutoFocus={(event) => event.preventDefault()}
        // A phone has no Escape: a tap anywhere but the textarea closes the
        // menu, and refocusing the same token must not bring it back.
        onPointerDownOutside={(event) => {
          if (!anchorRef.current?.contains(event.target as Node)) onDismiss();
        }}
        // Never wider than the textarea: with collisions off, nothing would
        // shift a wider menu back on screen.
        className="w-[min(40rem,var(--radix-popover-trigger-width))] p-0"
      >
        <MenuList {...list} />
      </PopoverContent>
    </Popover>
  );
}

function MenuList({ matches, activeIndex, loading, onHover, onChoose }: MenuListProps) {
  return (
    <Command shouldFilter={false} className="bg-transparent">
      {/* The 2px is the popover's border, so the list never overflows the space above. */}
      <CommandList className="max-h-[min(45dvh,18rem,calc(var(--radix-popover-content-available-height)-2px))]">
        <CommandEmpty>{loading ? "Loading commands…" : "No matching command."}</CommandEmpty>
        <CommandGroup>
          {matches.map((item, index) => (
            <CommandItem
              key={item.id}
              value={item.id}
              aria-selected={index === activeIndex}
              className={index === activeIndex ? "bg-accent text-accent-foreground" : undefined}
              onMouseDown={(event) => event.preventDefault()}
              onMouseMove={() => onHover(index)}
              onSelect={() => onChoose(item)}
            >
              <span className="line-clamp-2 min-w-0 flex-1">
                <span className="font-medium">{item.insertText}</span>
                {item.argsHint && (
                  <span className="text-muted-foreground ml-1">{item.argsHint}</span>
                )}
                {item.description && (
                  <span className="text-muted-foreground ml-2 text-xs">{item.description}</span>
                )}
              </span>
              {item.origin && (
                <span className="text-muted-foreground shrink-0 text-[11px]">[{item.origin}]</span>
              )}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </Command>
  );
}
