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
 * decides it; only a send the other way says so. Inverting with no choice to
 * make — idle, where a send starts a turn, or a harness that only queues — is
 * an ordinary send.
 */
export function deliveryFor({
  busy,
  steers,
  inverted,
}: {
  busy: boolean;
  steers: boolean;
  inverted: boolean;
}): Delivery | undefined {
  return inverted && offersChoice(busy, steers) ? "queue" : undefined;
}

export const DELIVERY_LABEL: Record<Delivery, { name: string; detail: string }> = {
  steer: { name: "Steer", detail: "Read at its next step" },
  queue: { name: "Queue", detail: "Run after this turn" },
};
