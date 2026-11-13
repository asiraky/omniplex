import type { ReactElement, RefObject } from "react";

import { ChunkFallback } from "~/components/ChunkFallback";
import { commandMenuList } from "~/components/lazyLists";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { useChunk, useIdlePrefetch } from "~/useChunk";

import type { CommandMenuListProps } from "./CommandMenuList";

/** The completion menu, in a popover above the textarea. On a phone it must
    not cover the textarea: the composer sits at the bottom of the screen, so
    anything anchored there hides what is being typed. */
export function CommandMenu({
  open,
  anchor,
  anchorRef,
  onDismiss,
  ...list
}: CommandMenuListProps & {
  open: boolean;
  /** The textarea the menu completes into. */
  anchor: ReactElement;
  anchorRef: RefObject<HTMLElement | null>;
  /** Keeps the menu shut for the current token. */
  onDismiss: () => void;
}) {
  // The rows are cmdk, fetched once the page is idle; when the first slash
  // beats them, a spinner holds the menu's place. The keys never needed them:
  // arrows, Enter and Escape are the textarea's, so nothing typed is lost.
  useIdlePrefetch(commandMenuList);
  const { loaded: List, failed } = useChunk(commandMenuList, open);
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
        {List ? (
          <List {...list} />
        ) : (
          <ChunkFallback failed={failed} className="p-3 text-[12px]" />
        )}
      </PopoverContent>
    </Popover>
  );
}
