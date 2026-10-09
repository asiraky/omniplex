import {
  useEffect,
  useRef,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";

import type { ThreadMeta } from "~/protocol";
import { dropPosition } from "~/threadOrder";

/** What a row needs to be dragged. */
export interface RowDrag {
  /** On the row's own surface: a mouse or pen drags the row directly. A
      finger does not — on touch that press is a scroll or a long-press. */
  onRowPointerDown: (e: ReactPointerEvent) => void;
  /** On the reorder handle: any pointer drags, and the arrow keys step. */
  handle: {
    onPointerDown: (e: ReactPointerEvent) => void;
    onKeyDown: (e: ReactKeyboardEvent) => void;
  };
}

// The pointer half of a drag, fetched once a sortable list is on screen
// rather than with the first paint. Until it lands a press on a row is only
// a click, which is all it would have been a second earlier anyway.
let session: typeof import("./dragSession") | undefined;
let loading: Promise<void> | undefined;
function loadSession() {
  loading ??= import("./dragSession").then(
    (m) => {
      session = m;
    },
    () => {
      // Offline, most likely: the next list to mount tries again.
      loading = undefined;
    },
  );
}

/**
 * One run of rows the user can put in their own order: a project's group, or
 * the whole list when it is flat. A drag never leaves its run, which is what
 * keeps a reorder inside one project when the list is grouped.
 *
 * Hand-rolled on pointer events rather than a library: it is one axis, one
 * list, and no drop targets, and every kilobyte here is paid on a phone over
 * 4G before the sidebar can paint. The pointer handling itself is in
 * `dragSession`, loaded after the first paint.
 */
export function SortableRows({
  rows,
  enabled,
  onReorder,
  children,
}: {
  rows: ThreadMeta[];
  /** Off while a delete holds the list still for its exit animation. */
  enabled: boolean;
  onReorder: (threadId: string, position: number) => void;
  children: (s: ThreadMeta, drag: RowDrag | undefined) => ReactNode;
}) {
  const container = useRef<HTMLDivElement>(null);
  // Tears down whatever drag is live, if the list unmounts under it — the
  // sheet closing, the breakpoint swapping the sidebar's shape.
  const stop = useRef<(() => void) | null>(null);
  useEffect(() => {
    loadSession();
    return () => stop.current?.();
  }, []);

  // A drag holds the rows' indexes from when it began. A row arriving or
  // leaving under it (a thread made on another device) would hand the pointer
  // to a different row, so the drag is dropped instead. Statuses change all
  // the time and move nothing, so only the order counts.
  const order = rows.map((s) => s.id).join(" ");
  useEffect(() => () => stop.current?.(), [order]);

  const move = (rowsAtStart: ThreadMeta[], id: string, to: number) => {
    const position = dropPosition(rowsAtStart, id, to);
    if (position !== null) onReorder(id, position);
  };

  const start = (e: ReactPointerEvent, id: string, anyPointer: boolean) => {
    if (!enabled || !session || e.button !== 0 || stop.current) return;
    if (!anyPointer && e.pointerType === "touch") return;
    const el = container.current;
    const rowsAtStart = rows;
    const from = rowsAtStart.findIndex((s) => s.id === id);
    if (!el || from < 0) return;
    // The handle is for nothing but dragging, so a finger on it must not
    // scroll the list or raise a long-press menu.
    if (anyPointer) e.preventDefault();
    stop.current = session.beginDrag(
      e,
      el,
      from,
      (to) => move(rowsAtStart, id, to),
      () => {
        stop.current = null;
      },
    );
  };

  // The handle answers the arrow keys too: one step per press, so the order
  // is reachable without a pointer at all.
  const step = (e: ReactKeyboardEvent, id: string) => {
    if (!enabled || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    e.preventDefault();
    const from = rows.findIndex((s) => s.id === id);
    const to = e.key === "ArrowUp" ? from - 1 : from + 1;
    if (from < 0 || to < 0 || to >= rows.length) return;
    move(rows, id, to);
  };

  return (
    <div ref={container}>
      {rows.map((s) => (
        <div key={s.id} data-reorder-id={s.id}>
          {children(
            s,
            enabled
              ? {
                  onRowPointerDown: (e) => start(e, s.id, false),
                  handle: {
                    onPointerDown: (e) => start(e, s.id, true),
                    onKeyDown: (e) => step(e, s.id),
                  },
                }
              : undefined,
          )}
        </div>
      ))}
    </div>
  );
}
