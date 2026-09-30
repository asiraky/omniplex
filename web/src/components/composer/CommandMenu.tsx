import type { ReactElement } from "react";

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "~/components/ui/sheet";
import type { ComposerItem } from "~/protocol";

interface MenuListProps {
  matches: ComposerItem[];
  activeIndex: number;
  loading: boolean;
  onHover: (index: number) => void;
  onChoose: (item: ComposerItem) => void;
}

/** The completion menu around the textarea: a popover above it on a desktop,
    a non-modal sheet from the bottom on a phone. */
export function CommandMenu({
  isDesktop,
  open,
  anchor,
  onDismiss,
  ...list
}: MenuListProps & {
  isDesktop: boolean;
  open: boolean;
  /** The textarea the menu completes into. */
  anchor: ReactElement;
  onDismiss: () => void;
}) {
  if (isDesktop) {
    return (
      <Popover open={open}>
        <PopoverAnchor asChild>{anchor}</PopoverAnchor>
        <PopoverContent
          side="top"
          align="start"
          onOpenAutoFocus={(event) => event.preventDefault()}
          className="w-[min(40rem,calc(100vw-2rem))] p-0"
        >
          <MenuList {...list} />
        </PopoverContent>
      </Popover>
    );
  }
  return (
    <>
      {anchor}
      <Sheet
        modal={false}
        open={open}
        onOpenChange={(next) => {
          if (!next) onDismiss();
        }}
      >
        <SheetContent
          side="bottom"
          onOpenAutoFocus={(event) => event.preventDefault()}
          className="max-h-[70dvh] p-0 pb-[env(safe-area-inset-bottom)]"
        >
          <SheetHeader>
            <SheetTitle>Commands</SheetTitle>
          </SheetHeader>
          <MenuList {...list} />
        </SheetContent>
      </Sheet>
    </>
  );
}

function MenuList({ matches, activeIndex, loading, onHover, onChoose }: MenuListProps) {
  return (
    <Command shouldFilter={false} className="bg-transparent">
      <CommandList className="max-h-[min(45dvh,18rem)]">
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
              <span className="min-w-0 flex-1">
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
