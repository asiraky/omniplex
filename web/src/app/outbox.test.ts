// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { createOutbox, useFailedSend, useWaitingSend } from "./outbox";

const send = (commandId: string, threadId = "t1", text = `text of ${commandId}`) => ({
  commandId,
  threadId,
  text,
  attachments: [],
});

describe("outbox", () => {
  it("holds a send as pending until its ack removes it", () => {
    const outbox = createOutbox();
    outbox.add(send("c1"));
    expect(outbox.get()).toEqual([{ ...send("c1"), status: "pending", sentAt: expect.any(Number) }]);

    expect(outbox.ack("c1")?.text).toBe("text of c1");
    expect(outbox.get()).toEqual([]);
    expect(outbox.ack("c1")).toBeUndefined();
  });

  it("hands back a failed send once, however many times it is reported", () => {
    const outbox = createOutbox();
    outbox.add(send("c1"));

    const failed = outbox.fail("c1", "timed out");
    expect(failed).toMatchObject({ commandId: "c1", status: "failed", reason: "timed out" });
    expect(outbox.fail("c1", "socket closed")).toBeUndefined();
    expect(outbox.get()[0]).toMatchObject({ reason: "timed out" });
    expect(outbox.fail("unknown", "x")).toBeUndefined();
  });

  it("puts a retried send back to pending, as the latest", () => {
    const outbox = createOutbox();
    outbox.add(send("c1"));
    outbox.add(send("c2"));
    outbox.fail("c1", "timed out");

    outbox.add(send("c1", "t1", "edited"));

    expect(outbox.get().map((e) => [e.commandId, e.status, e.text])).toEqual([
      ["c2", "pending", "text of c2"],
      ["c1", "pending", "edited"],
    ]);
    expect(outbox.get()[1]).not.toHaveProperty("reason");
    // Pending again, so it can fail again.
    expect(outbox.fail("c1", "again")).toBeDefined();
  });

  it("resends a failed entry as it went, steer or queue choice included, note dropped", () => {
    const outbox = createOutbox();
    outbox.add({ ...send("c1"), delivery: "queue" });
    outbox.fail("c1", "timed out");
    outbox.dismiss("c1");

    // As Retry hands it over: the failed entry itself.
    outbox.add(outbox.get()[0]);

    const [again] = outbox.get();
    expect(again).toMatchObject({ commandId: "c1", status: "pending", delivery: "queue" });
    expect(again).not.toHaveProperty("reason");
    expect(again).not.toHaveProperty("dismissed");
  });

  it("takes only the thread's failed sends", () => {
    const outbox = createOutbox();
    outbox.add(send("a1", "a"));
    outbox.add(send("a2", "a"));
    outbox.add(send("b1", "b"));
    outbox.fail("a1", "x");
    outbox.fail("b1", "x");

    expect(outbox.takeFailed("a").map((e) => e.commandId)).toEqual(["a1"]);
    expect(outbox.get().map((e) => e.commandId)).toEqual(["a2", "b1"]);
  });

  it("keeps a dismissed send, note put away, for a retry to find", () => {
    const outbox = createOutbox();
    outbox.add(send("c1"));
    outbox.dismiss("c1");
    expect(outbox.get()[0]).not.toHaveProperty("dismissed");

    outbox.fail("c1", "x");
    outbox.dismiss("c1");
    const dismissed = outbox.get();
    outbox.dismiss("c1");
    expect(outbox.get()).toBe(dismissed);
    expect(outbox.takeFailed("t1")).toMatchObject([{ commandId: "c1", dismissed: true }]);
  });

  it("drops the entries of threads that are gone", () => {
    const outbox = createOutbox([
      { ...send("a1", "a"), status: "pending", sentAt: 1 },
      { ...send("b1", "b"), status: "failed", reason: "x", sentAt: 2 },
    ]);
    const listener = vi.fn();
    outbox.subscribe(listener);

    outbox.prune(() => true);
    expect(listener).not.toHaveBeenCalled();

    outbox.prune((id) => id === "b");
    expect(outbox.get().map((e) => e.commandId)).toEqual(["b1"]);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("holds a send waiting on its uploads, then sends it under the same id", () => {
    const outbox = createOutbox();
    outbox.wait(send("c1"));
    expect(outbox.get()).toMatchObject([{ commandId: "c1", status: "uploading" }]);
    // Nothing to resend or take: it has not gone yet, and has not failed.
    expect(outbox.takeFailed("t1")).toEqual([]);

    outbox.add(send("c1"));
    expect(outbox.get()).toMatchObject([{ commandId: "c1", status: "pending" }]);
    expect(outbox.ack("c1")?.commandId).toBe("c1");
    expect(outbox.get()).toEqual([]);
  });

  it("fails a waiting send as an upload failure, with its files as they came out", () => {
    const outbox = createOutbox();
    outbox.wait(send("c1"));
    const chip = { key: "k1", name: "shot.png", previewUrl: "", status: "error" as const, error: "disk full" };

    const failed = outbox.fail("c1", "shot.png did not upload", {
      uploadFailed: true,
      attachments: [chip],
    });

    expect(failed).toMatchObject({ status: "failed", uploadFailed: true, attachments: [chip] });
    expect(outbox.get()).toEqual([failed]);
    expect(outbox.fail("c1", "again")).toBeUndefined();
    expect(outbox.fail("missing", "x", { uploadFailed: true })).toBeUndefined();
    // Sent again, it is an ordinary send: the upload failure was the old one's.
    outbox.add(send("c1"));
    expect(outbox.get()[0]).not.toHaveProperty("uploadFailed");
  });

  it("keeps the same snapshot while nothing changes", () => {
    const outbox = createOutbox();
    outbox.add(send("c1"));
    const before = outbox.get();
    const listener = vi.fn();
    outbox.subscribe(listener);

    outbox.ack("nope");
    outbox.dismiss("nope");
    outbox.takeFailed("t1");

    expect(outbox.get()).toBe(before);
    expect(listener).not.toHaveBeenCalled();
  });
});

describe("useFailedSend", () => {
  it("returns the thread's latest failure and keeps it through unrelated changes", () => {
    const outbox = createOutbox();
    let renders = 0;
    const hook = renderHook(() => {
      renders++;
      return useFailedSend(outbox, "a");
    });
    expect(hook.result.current).toBeUndefined();

    act(() => {
      outbox.add(send("a1", "a"));
      outbox.add(send("a2", "a"));
      outbox.fail("a1", "x");
      outbox.fail("a2", "y");
    });
    const latest = hook.result.current;
    expect(latest?.commandId).toBe("a2");
    const rendersAfterFail = renders;

    act(() => {
      outbox.add(send("b1", "b"));
      outbox.fail("b1", "z");
      outbox.add(send("a3", "a"));
    });
    expect(hook.result.current).toBe(latest);
    expect(renders).toBe(rendersAfterFail);

    act(() => outbox.dismiss("a2"));
    expect(hook.result.current?.commandId).toBe("a1");
    act(() => outbox.dismiss("a1"));
    expect(hook.result.current).toBeUndefined();
  });

  it("reads nothing for no thread", () => {
    const outbox = createOutbox([{ ...send("a1", "a"), status: "failed", reason: "x", sentAt: 1 }]);
    const hook = renderHook(() => useFailedSend(outbox, null));
    expect(hook.result.current).toBeUndefined();
  });
});

describe("useWaitingSend", () => {
  it("flips with the thread's waiting send, and not for another thread's", () => {
    const outbox = createOutbox();
    let renders = 0;
    const hook = renderHook(() => {
      renders++;
      return useWaitingSend(outbox, "a");
    });
    expect(hook.result.current).toBe(false);

    act(() => outbox.wait(send("a1", "a")));
    expect(hook.result.current).toBe(true);
    const rendersWaiting = renders;

    act(() => {
      outbox.wait(send("b1", "b"));
      outbox.add(send("a2", "a"));
    });
    expect(renders).toBe(rendersWaiting);

    act(() => outbox.add(send("a1", "a")));
    expect(hook.result.current).toBe(false);

    act(() => outbox.wait(send("a3", "a")));
    act(() => outbox.fail("a3", "x", { uploadFailed: true }));
    expect(hook.result.current).toBe(false);
  });

  it("reads false for no thread", () => {
    const outbox = createOutbox();
    outbox.wait(send("a1", "a"));
    expect(renderHook(() => useWaitingSend(outbox, null)).result.current).toBe(false);
  });
});
