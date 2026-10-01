// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
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
      begin={{ instanceId: "i1", methodId: "m1" }}
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

  it("finishes by itself on success when asked to, but not on an error", async () => {
    const t = wires();
    const onFinished = vi.fn();
    const ui = (
      <AuthFlowRun
        wires={t.w}
        begin={{ mcpServer: "srv", origin: "https://h.example" }}
        finishOnSuccess
        onFinished={onFinished}
        onClose={() => {}}
      />
    );
    render(ui);
    await t.ack("f1");
    t.emit({ flowId: "f1", error: "denied" });
    expect(onFinished).not.toHaveBeenCalled();

    const u = wires();
    const done = vi.fn();
    render(
      <AuthFlowRun wires={u.w} begin={{ cli: "gws", account: "work" }} finishOnSuccess onFinished={done} onClose={() => {}} />,
    );
    await u.ack("f2");
    t.emit({ flowId: "f1", done: true });
    u.emit({ flowId: "f2", done: true });
    expect(done).toHaveBeenCalledOnce();
    expect(u.command).toHaveBeenCalledWith("auth_begin", { cli: "gws", account: "work" });
  });

  it("does not restart the flow when re-rendered with an equal begin argument", async () => {
    const t = wires();
    const el = (begin: { instanceId: string; methodId: string }) => (
      <AuthFlowRun wires={t.w} begin={begin} onFinished={() => {}} onClose={() => {}} />
    );
    const r = render(el({ instanceId: "i1", methodId: "m1" }));
    r.rerender(wrap(el({ instanceId: "i1", methodId: "m1" })));
    expect(t.command.mock.calls.filter(([name]) => name === "auth_begin")).toHaveLength(1);
  });
});
