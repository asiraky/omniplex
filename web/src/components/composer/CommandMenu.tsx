import type { ReactElement, RefObject } from "react";

import { ChunkFallback } from "~/components/ChunkFallback";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { chunk } from "~/lib/chunk";
import { useChunk, useIdlePrefetch } from "~/useChunk";

import type { MenuListProps } from "./CommandMenuList";

// The list is built on cmdk, which stays out of the first load. The composer
// fetches it once the page is idle, so the first slash rarely waits; when it
// does, a spinner holds the menu's place. The keys never needed it: arrows,
// Enter and Escape are the textarea's, so nothing typed is lost meanwhile.
export const menuList = chunk(() => import("./CommandMenuList").then((m) => m.MenuList));

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
  useIdlePrefetch(menuList);
  const { loaded: MenuList, failed } = useChunk(menuList, open);
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
        {MenuList ? (
          <MenuList {...list} />
        ) : (
          <ChunkFallback failed={failed} className="p-3 text-[12px]" />
        )}
      </PopoverContent>
    </Popover>
  );
}
