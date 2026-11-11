// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { useThreadSelection } from "./useThreadSelection";
import type { Wire } from "./useWire";

const threads = [{ id: "t1", projectId: "p1", phase: "idle" }] as ThreadMeta[];

function setup() {
  const client = { attach: vi.fn(), detach: vi.fn() };
  const wire = {
    clientRef: { current: client },
    activeRef: { current: null },
    threads,
    // Not loaded, so the restore stays out of it.
    threadsLoaded: false,
    setState: vi.fn(),
    dropResume: vi.fn(),
    resume: null,
    link: null,
  } as unknown as Wire;
  return renderHook(() =>
    useThreadSelection({
      wire,
      isDesktop: true,
      onLeave: vi.fn(),
      onForget: vi.fn(),
      onOpenLink: vi.fn(),
    }),
  );
}

beforeEach(() => localStorage.clear());

describe("starting a new thread", () => {
  it("opens on the open thread's project when nothing more is asked", () => {
    const { result } = setup();
    act(() => result.current.select("t1"));
    act(() => result.current.startNew());
    expect(result.current.creating).toMatchObject({ projectId: "p1" });
    expect(result.current.activeId).toBeNull();
  });

  it("carries what the request asked for, the project falling back to the open thread's", () => {
    const { result } = setup();
    act(() => result.current.select("t1"));
    act(() => result.current.startNew({ folderId: "f1", kind: "attach", attachPath: "/w", focus: true }));
    expect(result.current.creating).toMatchObject({
      projectId: "p1",
      folderId: "f1",
      kind: "attach",
      attachPath: "/w",
      focus: true,
    });

    act(() => result.current.startNew({ projectId: "p2" }));
    expect(result.current.creating).toMatchObject({ projectId: "p2" });
    expect(result.current.creating?.folderId).toBeUndefined();
  });

  it("is a new request every time, even with a draft already open", () => {
    const { result } = setup();
    act(() => result.current.startNew({ projectId: "p1" }));
    const first = result.current.creating!.nonce;
    act(() => result.current.startNew({ projectId: "p1" }));
    expect(result.current.creating!.nonce).not.toBe(first);
  });

  it("keeps the draft on a project pick, dropping what belonged to the old project", () => {
    const { result } = setup();
    act(() => result.current.startNew({ projectId: "p1", folderId: "f1", kind: "attach", attachPath: "/w" }));
    const nonce = result.current.creating!.nonce;
    act(() => result.current.pickDraftProject("p2"));
    expect(result.current.creating).toEqual({ projectId: "p2", nonce });
  });
});
