import { useCallback, useMemo, useState } from "react";

import type { AuthFlowEvent } from "~/protocol";

import type { Wire } from "./useWire";

export type ProviderAuth = ReturnType<typeof useProviderAuth>;

/**
 * Signing in to a provider instance, and asking the server to look at the
 * installed harnesses again afterwards.
 */
export function useProviderAuth(wire: Wire) {
  const { clientRef, harnesses, setHarnesses } = wire;
  // The instance whose sign-in terminal is open, if any. Closing it rechecks,
  // so the login shows up as "ready" by itself.
  const [loginInstance, setLoginInstance] = useState<string | null>(null);
  // The instance the structured sign-in dialog is open for.
  const [authInstance, setAuthInstance] = useState<string | null>(null);

  // Ask the server to re-probe, for when the user has just installed something.
  const recheck = useCallback(() => {
    // Returned so a caller with a spinner can hold it up until the answer.
    return clientRef.current?.command("recheck_harnesses", {}).then((res) => {
      if (res?.harnesses) setHarnesses(res.harnesses);
    });
  }, [clientRef, setHarnesses]);

  // What the providers surface needs from the client: commands, and the
  // auth-flow event stream (which deliberately bypasses the state reducer:
  // flows are ephemeral and their frames can carry nothing persistable).
  const authWires = useMemo(
    () => ({
      command: (cmd: string, args: unknown) => clientRef.current!.command(cmd, args),
      subscribe: (flowId: string, listener: (ev: AuthFlowEvent) => void) =>
        clientRef.current?.onAuthFlow(flowId, listener) ?? (() => {}),
    }),
    [clientRef],
  );

  const instanceName = useCallback(
    (instanceId: string) =>
      harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === instanceId)?.displayName ??
      instanceId,
    [harnesses],
  );

  // Every "sign in" affordance routes through here: a flows-capable instance
  // gets the structured dialog, anything else the embedded login terminal.
  const openInstanceAuth = useCallback(
    (instanceId: string) => {
      const inst = harnesses.flatMap((h) => h.instances ?? []).find((i) => i.id === instanceId);
      if (inst?.auth === "flows") setAuthInstance(instanceId);
      else setLoginInstance(instanceId);
    },
    [harnesses],
  );

  return {
    loginInstance,
    setLoginInstance,
    authInstance,
    setAuthInstance,
    recheck,
    authWires,
    instanceName,
    openInstanceAuth,
  };
}
