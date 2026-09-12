// How the next message reaches a busy harness, and the memory of what was
// picked last. Per browser and per session, like the draft: on the phone the
// habit is usually "interrupt", at the desk usually "now", and the choice is
// about how this session is being worked, not something worth syncing.

import type { Delivery } from "~/protocol";

export const DELIVERY_OPTIONS: { id: Delivery; label: string; description: string }[] = [
  {
    id: "now",
    label: "Send now",
    description: "The model reads it after its current step",
  },
  {
    id: "interrupt",
    label: "Interrupt",
    description: "Stop what it is doing and take this first",
  },
  {
    id: "end",
    label: "When it is done",
    description: "Held back until the work really finishes — not on a question or an error",
  },
];

export const DEFAULT_DELIVERY: Delivery = "now";

export function deliveryLabel(id: Delivery): string {
  return DELIVERY_OPTIONS.find((o) => o.id === id)?.label ?? "Send now";
}

const KEY = (sessionId: string) => `omniplex.delivery.v1:${sessionId}`;

/** The delivery last chosen in this session, or the default. */
export function loadDelivery(sessionId: string): Delivery {
  if (!sessionId) return DEFAULT_DELIVERY;
  try {
    const raw = localStorage.getItem(KEY(sessionId));
    return DELIVERY_OPTIONS.some((o) => o.id === raw) ? (raw as Delivery) : DEFAULT_DELIVERY;
  } catch {
    // Storage can be denied outright (Safari private mode); the picker still
    // works, it just starts from the default each load.
    return DEFAULT_DELIVERY;
  }
}

export function saveDelivery(sessionId: string, delivery: Delivery) {
  if (!sessionId) return;
  try {
    localStorage.setItem(KEY(sessionId), delivery);
  } catch {
    // Costs the memory, not the interaction.
  }
}
