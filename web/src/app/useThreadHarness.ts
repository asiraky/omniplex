import { useCallback, useMemo } from "react";
import { toast } from "sonner";

import type { ThreadMeta, Turn } from "~/protocol";

import type { Wire } from "./useWire";

export type ThreadHarness = ReturnType<typeof useThreadHarness>;

/**
 * The attached thread's harness and what can be changed about it: the
 * account it runs on, its model, effort and permission mode, and re-sending a
 * turn that failed.
 */
export function useThreadHarness(
  wire: Wire,
  activeId: string | null,
  meta: ThreadMeta | undefined,
) {
  const { clientRef, harnesses, state } = wire;
  const activeProviderInstance = harnesses
    .flatMap((h) => h.instances ?? [])
    .find((i) => i.id === (meta?.providerInstance || meta?.harness));

  // The permission modes for the attached thread's harness. Everything the UI
  // knows about them came from the adapter via the server; ids stay opaque.
  const modeOptions = useMemo(
    () => harnesses.find((h) => h.id === state?.harness)?.permissionModes ?? [],
    [harnesses, state?.harness],
  );
  // An empty recorded mode means the harness default; render it as such.
  const currentModeId =
    (modeOptions.some((m) => m.id === state?.mode) ? state?.mode : undefined) ??
    modeOptions.find((m) => m.default)?.id ??
    modeOptions[0]?.id ??
    "";

  const switchMode = useCallback(
    (modeId: string) => {
      if (!activeId) return;
      // Every mode switches the same way: the picked value is the decision.
      clientRef.current?.command("set_mode", { threadId: activeId, mode: modeId }).catch((e) => {
        toast.error("Could not switch permission mode", { description: e.message });
      });
    },
    [activeId, clientRef],
  );

  const switchModel = useCallback(
    (modelId: string) => {
      if (!activeId) return;
      clientRef.current?.command("set_model", { threadId: activeId, model: modelId }).catch((e) => {
        toast.error("Could not switch model", { description: e.message });
      });
    },
    [activeId, clientRef],
  );
  const switchEffort = useCallback(
    (effort: string) => {
      if (!activeId) return;
      clientRef.current?.command("set_effort", { threadId: activeId, effort }).catch((e) => {
        toast.error("Could not change reasoning effort", { description: e.message });
      });
    },
    [activeId, clientRef],
  );
  // Re-sends a failed turn's prompt, images and all. Only ever on an explicit
  // press: nothing finishing (a sign-in, an account switch) resends by itself.
  const retryTurn = useCallback(
    (turn: Turn) => {
      if (!activeId) return;
      clientRef.current
        ?.command("prompt", {
          threadId: activeId,
          text: turn.prompt,
          ...(turn.images?.length ? { imageIds: turn.images.map((i) => i.id) } : {}),
        })
        .catch((e) => toast.error("Could not send", { description: e.message }));
    },
    [activeId, clientRef],
  );
  // The thread's harness's other accounts that could take the next turn: the
  // way out of a usage limit.
  const activeInstanceId = activeProviderInstance?.id;
  const switchTargets = useMemo(
    () =>
      (harnesses.find((h) => h.id === state?.harness)?.instances ?? [])
        .filter(
          (i) =>
            i.enabled !== false && i.availability?.state === "ready" && i.id !== activeInstanceId,
        )
        .map((i) => ({ id: i.id, name: i.displayName })),
    [harnesses, state?.harness, activeInstanceId],
  );
  // Moves the thread, conversation and all, to another account of its
  // harness. From the model picker it asks first (a picker row is an easy
  // thing to tap by accident) and may bring a model along; from the limit
  // card, where the button says exactly what it does, it goes straight on to
  // retry the prompt that hit the limit.
  const switchAccount = useCallback(
    async (
      instance: string,
      opts: { model?: string; retry?: Turn; confirm?: boolean } = {},
    ): Promise<boolean> => {
      if (!activeId || !clientRef.current) return false;
      const name =
        harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === instance)?.displayName ??
        instance;
      if (
        opts.confirm &&
        !window.confirm(
          `Move this thread to ${name}?\n\nThe conversation comes with it; the next turn runs on ${name}.`,
        )
      ) {
        return false;
      }
      try {
        await clientRef.current.command("switch_account", { threadId: activeId, instance });
      } catch (e) {
        toast.error("Could not switch account", { description: (e as Error).message });
        return false;
      }
      if (opts.model && opts.model !== state?.model) switchModel(opts.model);
      if (opts.retry) retryTurn(opts.retry);
      return true;
    },
    [activeId, clientRef, harnesses, state?.model, switchModel, retryTurn],
  );

  return {
    activeProviderInstance,
    modeOptions,
    currentModeId,
    switchMode,
    switchModel,
    switchEffort,
    retryTurn,
    switchTargets,
    switchAccount,
  };
}
