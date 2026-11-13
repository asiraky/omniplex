// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { alertSoundOn, playAlertSound } from "~/lib/alertSound";
import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import { useBackgroundAlerts } from "./useBackgroundAlerts";

vi.mock("~/lib/toast", () => ({
  toast: { success: vi.fn(), info: vi.fn(), warning: vi.fn(), error: vi.fn(), dismiss: vi.fn() },
}));
vi.mock("~/lib/alertSound", () => ({ alertSoundOn: vi.fn(() => false), playAlertSound: vi.fn() }));

type Attention = ThreadMeta["attention"];
const thread = (id: string, attention: Attention) => ({ id, title: id, attention }) as ThreadMeta;

interface Props {
  threads: ThreadMeta[];
  loaded?: boolean;
  activeId?: string | null;
  covered?: boolean;
}

function setup(first: Props) {
  const open = vi.fn();
  const hook = renderHook(
    ({ threads, loaded = true, activeId = null, covered = false }: Props) =>
      useBackgroundAlerts({ threads, loaded, activeId, covered, open }),
    { initialProps: first },
  );
  return { open, rerender: (p: Props) => hook.rerender(p) };
}

/** Every toast raised, whatever its variant, in the order raised. */
const raised = () =>
  [toast.success, toast.info, toast.warning, toast.error]
    .flatMap((f) => {
      const { calls, invocationCallOrder } = vi.mocked(f).mock;
      return calls.map((call, i) => ({ call, order: invocationCallOrder[i] }));
    })
    .sort((x, y) => x.order - y.order)
    .map((c) => c.call);

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(alertSoundOn).mockReturnValue(false);
});

describe("useBackgroundAlerts", () => {
  it("says nothing about the first list, whatever state it is in", () => {
    const { rerender } = setup({ threads: [], loaded: false });
    rerender({ threads: [thread("a", "needs_permission"), thread("b", "needs_prompt")] });
    expect(raised()).toHaveLength(0);
  });

  it("raises a toast when a thread in the background finishes", () => {
    const { rerender } = setup({ threads: [thread("a", "working")], activeId: "b" });
    rerender({ threads: [thread("a", "needs_prompt")], activeId: "b" });
    expect(toast.success).toHaveBeenCalledTimes(1);
  });

  it("keeps one toast per thread: a newer alert replaces the last", () => {
    const { rerender } = setup({ threads: [thread("a", "working"), thread("b", "working")] });
    rerender({ threads: [thread("a", "needs_permission"), thread("b", "needs_prompt")] });
    rerender({ threads: [thread("a", "working"), thread("b", "needs_prompt")] });
    rerender({ threads: [thread("a", "needs_prompt"), thread("b", "needs_prompt")] });
    const ids = raised().map((c) => (c[1] as { id: string }).id);
    expect(ids).toHaveLength(3);
    expect(ids[0]).toBe(ids[2]);
    expect(ids[1]).not.toBe(ids[0]);
  });

  it("stays quiet about the thread on screen", () => {
    const { rerender } = setup({ threads: [thread("a", "working")], activeId: "a" });
    rerender({ threads: [thread("a", "needs_prompt")], activeId: "a" });
    expect(raised()).toHaveLength(0);
  });

  it("speaks up about the thread on screen while something covers it", () => {
    const { rerender } = setup({ threads: [thread("a", "working")], activeId: "a", covered: true });
    rerender({ threads: [thread("a", "needs_answer")], activeId: "a", covered: true });
    expect(raised()).toHaveLength(1);
  });

  it("does not alert later for a change it saw while on screen", () => {
    const { rerender } = setup({ threads: [thread("a", "working")], activeId: "a" });
    rerender({ threads: [thread("a", "needs_prompt")], activeId: "a" });
    rerender({ threads: [thread("a", "needs_prompt")], activeId: "b" });
    expect(raised()).toHaveLength(0);
  });

  it("opens the thread from the toast's action", () => {
    const { rerender, open } = setup({ threads: [thread("a", "working")] });
    rerender({ threads: [thread("a", "needs_permission")] });
    const opts = vi.mocked(toast.warning).mock.calls[0][1] as unknown as {
      action: { onClick: () => void };
    };
    opts.action.onClick();
    expect(open).toHaveBeenCalledWith("a");
  });

  it("takes the toast down on arriving at its thread", () => {
    const threads = [thread("a", "needs_permission")];
    const { rerender } = setup({ threads: [thread("a", "working")] });
    rerender({ threads });
    expect(toast.dismiss).not.toHaveBeenCalled();
    rerender({ threads, activeId: "a", covered: true });
    expect(toast.dismiss).not.toHaveBeenCalled();
    rerender({ threads, activeId: "a" });
    expect(toast.dismiss).toHaveBeenCalledWith("thread-alert:a");
  });

  it("takes the toast down when its thread goes away", () => {
    const { rerender } = setup({ threads: [thread("a", "working"), thread("b", "working")] });
    rerender({ threads: [thread("a", "needs_prompt"), thread("b", "working")] });
    rerender({ threads: [thread("b", "working")] });
    expect(toast.dismiss).toHaveBeenCalledWith("thread-alert:a");
  });

  it("plays no sound when the sound is off", () => {
    const { rerender } = setup({ threads: [thread("a", "working")] });
    rerender({ threads: [thread("a", "needs_prompt")] });
    expect(playAlertSound).not.toHaveBeenCalled();
  });

  it("plays one sound for a list carrying two alerts, the one that needs you", () => {
    vi.mocked(alertSoundOn).mockReturnValue(true);
    const { rerender } = setup({ threads: [thread("a", "working"), thread("b", "working")] });
    rerender({ threads: [thread("a", "needs_prompt"), thread("b", "needs_permission")] });
    expect(raised()).toHaveLength(2);
    expect(playAlertSound).toHaveBeenCalledTimes(1);
    expect(playAlertSound).toHaveBeenCalledWith("needs");
  });

  it("plays no sound for a list that raised nothing", () => {
    vi.mocked(alertSoundOn).mockReturnValue(true);
    const { rerender } = setup({ threads: [thread("a", "working")], activeId: "a" });
    rerender({ threads: [thread("a", "needs_prompt")], activeId: "a" });
    expect(playAlertSound).not.toHaveBeenCalled();
  });
});
