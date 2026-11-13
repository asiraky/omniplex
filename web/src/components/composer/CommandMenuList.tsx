import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import type { ComposerItem } from "~/protocol";

export interface MenuListProps {
  matches: ComposerItem[];
  activeIndex: number;
  loading: boolean;
  onHover: (index: number) => void;
  onChoose: (item: ComposerItem) => void;
}

/** The slash menu's rows. The textarea owns the keys; this only shows them. */
export function MenuList({ matches, activeIndex, loading, onHover, onChoose }: MenuListProps) {
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
