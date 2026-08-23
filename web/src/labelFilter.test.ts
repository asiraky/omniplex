import { describe, expect, it } from "vitest";

import { UNLABELLED, visibleSessions } from "./labelFilter";
import type { Label, SessionMeta } from "~/protocol";

const label = (id: string): Label => ({ id, name: id, color: "#000", position: 0, createdAt: 0 });
const session = (id: string, labelId?: string) => ({ id, labelId }) as SessionMeta;

const labels = [label("l1"), label("l2")];
const sessions = [session("a", "l1"), session("b", "l2"), session("c"), session("d", "gone")];
const ids = (list: SessionMeta[]) => list.map((s) => s.id);

describe("visibleSessions", () => {
  it("shows everything when nothing is switched off", () => {
    expect(visibleSessions(sessions, labels, new Set())).toBe(sessions);
  });

  it("drops the sessions filed under a hidden label", () => {
    expect(ids(visibleSessions(sessions, labels, new Set(["l1"])))).toEqual(["b", "c", "d"]);
  });

  it("treats unlabelled as its own switch, and a dangling label as unlabelled", () => {
    // "d" points at a label that no longer exists — the deletion broadcast can
    // land before the reassignment does, and it is unfiled in the meantime.
    expect(ids(visibleSessions(sessions, labels, new Set([UNLABELLED])))).toEqual(["a", "b"]);
  });

  it("ignores hidden ids whose label is gone, rather than stranding sessions", () => {
    expect(visibleSessions(sessions, labels, new Set(["deleted-label"]))).toBe(sessions);
  });

  it("can hide everything, which the sidebar renders as its own empty state", () => {
    expect(visibleSessions(sessions, labels, new Set(["l1", "l2", UNLABELLED]))).toEqual([]);
  });
});
