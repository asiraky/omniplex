// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { AuthFlowRun, type AuthWires } from "./AuthFlowDialog";
import type { AuthFlowEvent } from "~/protocol";

/** Wires whose auth_begin ack is released by the test, so it can land before
    or after the dialog closes. */
function wires() {
  let ack: (result: { flowId: string }) => void = () => {};
  let listener: ((ev: AuthFlowEvent) => void) | null = null;
  const unsubscribe = vi.fn();
  const command = vi.fn((name: string, _args: unknown) =>
    name === "auth_begin"
      ? new Promise<{ flowId: string }>((resolve) => (ack = resolve))
      : Promise.resolve({}),
  );
  const subscribe = vi.fn((_flowId: string, l: (ev: AuthFlowEvent) => void) => {
    listener = l;
    return unsubscribe;
  });
  const w: AuthWires = { command, subscribe };
  return {
    w,
    command,
    subscribe,
    unsubscribe,
    ack: async (flowId: string) => {
      await act(async () => ack({ flowId }));
    },
    emit: (ev: AuthFlowEvent) => act(() => listener?.(ev)),
  };
}

function run(w: AuthWires) {
  return render(
    <AuthFlowRun
      wires={w}
      instanceId="i1"
      methodId="m1"
      onFinished={() => {}}
      onClose={() => {}}
    />,
  );
}

describe("AuthFlowRun", () => {
  it("answers the prompt on the flow the server started", async () => {
    const t = wires();
    run(t.w);
    await t.ack("f1");
    t.emit({ flowId: "f1", prompt: { id: "p1", message: "Paste the code" } });

    fireEvent.change(screen.getByLabelText("Paste the code"), { target: { value: "abc" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));

    expect(t.command).toHaveBeenCalledWith("auth_respond", {
      flowId: "f1",
      promptId: "p1",
      value: "abc",
    });
  });

  it("cancels a flow whose ack lands after the dialog closed, without listening to it", async () => {
    const t = wires();
    const r = run(t.w);
    r.unmount();
    await t.ack("f1");

    expect(t.subscribe).not.toHaveBeenCalled();
    expect(t.command).toHaveBeenCalledWith("auth_cancel", { flowId: "f1" });
  });

  it("stops listening and cancels an unfinished flow on close", async () => {
    const t = wires();
    const r = run(t.w);
    await t.ack("f1");
    r.unmount();

    expect(t.unsubscribe).toHaveBeenCalledOnce();
    expect(t.command).toHaveBeenCalledWith("auth_cancel", { flowId: "f1" });
  });

  it("leaves a finished flow alone on close", async () => {
    const t = wires();
    const r = run(t.w);
    await t.ack("f1");
    t.emit({ flowId: "f1", done: true });
    r.unmount();

    expect(t.unsubscribe).toHaveBeenCalledOnce();
    expect(t.command).not.toHaveBeenCalledWith("auth_cancel", expect.anything());
  });
});
