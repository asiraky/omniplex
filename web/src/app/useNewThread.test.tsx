// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CompareRun, NewThreadInput } from "~/components/ThreadDraft";
import { toast } from "~/lib/toast";

import { NEW_THREAD } from "./threadKeys";
import type { ComposerDrafts } from "./useComposerDrafts";
import { useCompareThreads } from "./useNewThread";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const run = (model: string, label: string): CompareRun => ({
  input: { model, text: "fix the bug", workspace: "managed" } as NewThreadInput,
  label,
});
const runs = [run("opus", "Opus 5"), run("sonnet", "Sonnet 5"), run("gpt", "GPT-5.5")];

type Reply = { threadId: string; promptError?: string };

/** A server that answers each create when the test says so. */
function setup() {
  const calls: { model: string; resolve: (r: Reply) => void; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: NewThreadInput) =>
        new Promise<Reply>((resolve, reject) => calls.push({ model: args.model, resolve, reject })),
    ),
  };
  const wire = { clientRef: { current: client } } as unknown as Wire;
  const setDraft = vi.fn();
  const select = vi.fn();
  const store = { setDraft } as unknown as ComposerDrafts;
  const { result } = renderHook(() => useCompareThreads({ wire, store, select }));
  return { compare: result.current, calls, setDraft, select };
}

beforeEach(() => vi.clearAllMocks());

describe("useCompareThreads", () => {
  it("sends every create before any answers, then opens the draft's own model", async () => {
    const { compare, calls, setDraft, select } = setup();
    let done!: Promise<void>;
    act(() => {
      done = compare(runs);
    });
    expect(calls.map((c) => c.model)).toEqual(["opus", "sonnet", "gpt"]);
    // The last to answer is the first run: it still opens, not whichever came back first.
    await act(async () => {
      calls[2].resolve({ threadId: "t3" });
      calls[1].resolve({ threadId: "t2" });
      calls[0].resolve({ threadId: "t1" });
      await done;
    });
    expect(select).toHaveBeenCalledExactlyOnceWith("t1");
    expect(setDraft).toHaveBeenCalledExactlyOnceWith(NEW_THREAD, "");
    expect(toast.success).toHaveBeenCalledWith("Started 3 threads");
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("opens the first that started and names the ones that did not", async () => {
    const { compare, calls, setDraft, select } = setup();
    let done!: Promise<void>;
    act(() => {
      done = compare(runs);
    });
    await act(async () => {
      calls[0].reject(new Error("no such model"));
      calls[1].resolve({ threadId: "t2" });
      calls[2].reject(new Error("rate limited"));
      await done;
    });
    expect(select).toHaveBeenCalledExactlyOnceWith("t2");
    expect(setDraft).toHaveBeenCalledExactlyOnceWith(NEW_THREAD, "");
    // One started: nothing to count.
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledExactlyOnceWith("Didn't start: Opus 5, GPT-5.5", {
      description: "no such model",
    });
  });

  it("throws the first failure when none started, leaving the draft alone", async () => {
    const { compare, calls, setDraft, select } = setup();
    let done!: Promise<void>;
    act(() => {
      done = compare(runs);
    });
    const settled = done.catch((e: Error) => e);
    await act(async () => {
      calls[1].reject(new Error("offline"));
      calls[0].reject(new Error("socket closed"));
      calls[2].reject(new Error("offline"));
    });
    expect(await settled).toEqual(new Error("socket closed"));
    expect(setDraft).not.toHaveBeenCalled();
    expect(select).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("keeps an unsent message in each thread it did not reach, with one toast", async () => {
    const { compare, calls, setDraft } = setup();
    let done!: Promise<void>;
    act(() => {
      done = compare(runs);
    });
    await act(async () => {
      calls[0].resolve({ threadId: "t1", promptError: "harness crashed" });
      calls[1].resolve({ threadId: "t2" });
      calls[2].resolve({ threadId: "t3", promptError: "harness crashed" });
      await done;
    });
    expect(setDraft).toHaveBeenCalledWith("t1", "fix the bug");
    expect(setDraft).toHaveBeenCalledWith("t3", "fix the bug");
    expect(setDraft).not.toHaveBeenCalledWith("t2", expect.anything());
    expect(toast.error).toHaveBeenCalledExactlyOnceWith(
      "The message did not send to Opus 5, GPT-5.5",
      { description: "harness crashed" },
    );
  });
});
