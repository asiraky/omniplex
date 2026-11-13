import { describe, expect, it } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { alertFor, alertToast, type AlertKind } from "./backgroundAlerts";

type Attention = ThreadMeta["attention"];

const meta = (attention: Attention, extra: Partial<ThreadMeta> = {}) =>
  ({ id: "a", title: "Fix the build", attention, ...extra }) as ThreadMeta;

const kind = (prev: Attention | null, next: Attention, extra?: Partial<ThreadMeta>) =>
  alertFor(prev === null ? undefined : meta(prev), meta(next, extra))?.kind ?? null;

describe("alertFor", () => {
  it("never alerts on a first sighting", () => {
    expect(kind(null, "needs_permission")).toBeNull();
    expect(kind(null, "needs_prompt")).toBeNull();
    expect(kind(null, "failed")).toBeNull();
  });

  it.each<[Attention, Attention, AlertKind]>([
    ["working", "needs_permission", "permission"],
    ["needs_prompt", "needs_permission", "permission"],
    ["working", "needs_answer", "question"],
    ["needs_permission", "needs_answer", "question"],
    ["working", "failed", "failed"],
    ["needs_prompt", "failed", "failed"],
    ["working", "needs_prompt", "finished"],
    ["background", "needs_prompt", "finished"],
  ])("%s -> %s is %s", (prev, next, want) => {
    expect(kind(prev, next)).toBe(want);
  });

  it("reports a turn that ended in an error as failed", () => {
    expect(kind("working", "needs_prompt", { turnFailed: true })).toBe("failed");
    expect(kind("background", "needs_prompt", { turnFailed: true })).toBe("failed");
  });

  it.each<[Attention, Attention]>([
    // Still waiting: it already said so.
    ["needs_permission", "needs_permission"],
    ["needs_answer", "needs_answer"],
    ["failed", "failed"],
    // Answered, here or on another device.
    ["needs_permission", "working"],
    ["needs_permission", "needs_prompt"],
    ["needs_answer", "needs_prompt"],
    // Nothing happened, or nothing worth saying.
    ["needs_prompt", "needs_prompt"],
    ["needs_prompt", "working"],
    ["working", "background"],
    ["working", "closed"],
    [undefined, "needs_prompt"],
  ])("%s -> %s says nothing", (prev, next) => {
    expect(kind(prev, next)).toBeNull();
  });

  it("does not repeat a failure when an idle thread's flag stays set", () => {
    const idle = meta("needs_prompt", { turnFailed: true });
    expect(alertFor(idle, { ...idle })).toBeNull();
  });
});

describe("alertToast", () => {
  it("names the thread, or says it has no name", () => {
    const named = (title: string) =>
      alertToast({ kind: "finished" }, meta("needs_prompt", { title })).description;
    expect(named("  Fix it  ")).toBe("Fix it");
    expect(named("   ")).toBe("Untitled thread");
  });
});
