import { useCallback } from "react";

import { toast } from "~/lib/toast";
import type { Label } from "~/protocol";

import type { Wire } from "./useWire";

export type LabelActions = ReturnType<typeof useLabelActions>;

/**
 * Label mutations. They fire and forget: the authoritative answer arrives as a
 * labels (or threads) broadcast, the same way it does for a paired device, so
 * there is no local state to reconcile, only failures to report.
 */
export function useLabelActions(wire: Wire) {
  const { clientRef, setLabels } = wire;
  const setThreadLabel = useCallback(
    (threadId: string, labelId: string) => {
      clientRef.current?.command("set_thread_label", { threadId, labelId }).catch((e) => {
        toast.error("Could not label that thread", { description: e.message });
      });
    },
    [clientRef],
  );
  const createLabel = useCallback(
    (name: string, color: string) => {
      clientRef.current?.command("create_label", { name, color }).catch((e) => {
        toast.error("Could not create that label", { description: e.message });
      });
    },
    [clientRef],
  );
  const saveLabel = useCallback(
    (label: Label) => {
      // Apply locally before the round-trip: a second edit made before the
      // broadcast lands (recolour, then flip the collapse switch) must derive
      // from this save, not from the stale snapshot, or the later save silently
      // reverts the earlier field. The broadcast then settles the true state.
      setLabels((ls) =>
        ls
          .map((l) => (l.id === label.id ? label : l))
          .sort(
            (a, b) =>
              a.position - b.position || a.createdAt - b.createdAt || (a.id < b.id ? -1 : 1),
          ),
      );
      clientRef.current
        ?.command("save_label", {
          labelId: label.id,
          name: label.name,
          color: label.color,
          position: label.position,
        })
        .catch((e) => {
          toast.error("Could not save that label", { description: e.message });
        });
    },
    [clientRef, setLabels],
  );
  const deleteLabel = useCallback(
    (id: string) => {
      clientRef.current?.command("delete_label", { labelId: id }).catch((e) => {
        toast.error("Could not delete that label", { description: e.message });
      });
    },
    [clientRef],
  );
  return { setThreadLabel, createLabel, saveLabel, deleteLabel };
}
