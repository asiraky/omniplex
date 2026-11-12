import type { Delivery } from "~/protocol";

/** Whether there is a choice to offer: only while a turn runs, and only where
    steering is possible at all. The default is then to steer, so the other
    way is always the queue. */
export function offersChoice(busy: boolean, steers: boolean): boolean {
  return busy && steers;
}

/**
 * The delivery to send with, or undefined for the server's default. The
 * default is never spelled out, so the server stays the one place that
 * decides it; only a send the other way says so. That holds even when this
 * client thinks the thread idle: a turn it has just started may not have
 * reached it yet, and the server, which knows, starts a turn with a queued
 * send into an idle thread anyway. A harness that only queues has no other
 * way to send.
 */
export function deliveryFor({
  steers,
  inverted,
}: {
  steers: boolean;
  inverted: boolean;
}): Delivery | undefined {
  return inverted && steers ? "queue" : undefined;
}

export const DELIVERY_LABEL: Record<Delivery, { name: string; detail: string }> = {
  steer: { name: "Steer", detail: "Read at its next step" },
  queue: { name: "Queue", detail: "Run after this turn" },
};
