import { describe, expect, it } from "vitest";

import type { ComposerItem } from "~/protocol";
import {
  detectComposerTrigger,
  rankComposerItems,
  replaceComposerTrigger,
  submittedComposerAction,
} from "./composerItems";

const items: ComposerItem[] = [
  { id: "model", name: "model", kind: "command", trigger: "/", insertText: "/model", behavior: "client-action", action: "model" },
  { id: "review", name: "review-code", description: "Inspect changes", kind: "skill", trigger: "$", insertText: "$review-code", behavior: "prompt", inline: true },
];

// A harness whose slash skills the model acts on mid-prompt, next to entries
// that only mean something as the first thing in one.
const slash: ComposerItem[] = [
  { id: "model", name: "model", kind: "command", trigger: "/", insertText: "/model", behavior: "client-action", action: "model" },
  { id: "compact", name: "compact", kind: "command", trigger: "/", insertText: "/compact", behavior: "adapter-action", action: "compact" },
  { id: "review", name: "review", description: "Inspect the diff", kind: "skill", trigger: "/", insertText: "/review", behavior: "prompt", inline: true },
  { id: "bro", name: "bro", description: "Review like a bro", kind: "skill", trigger: "/", insertText: "/bro", behavior: "prompt" },
];
const at = (text: string, list = slash) => {
  const cursor = text.indexOf("|");
  return detectComposerTrigger(text.replace("|", ""), cursor, list);
};
const ids = (text: string) => {
  const trigger = at(text);
  return trigger ? rankComposerItems(slash, trigger).map((item) => item.id) : null;
};

describe("composer item logic", () => {
  it("detects a trigger at the start of the token under the cursor", () => {
    expect(detectComposerTrigger("/mo", 3, items)).toMatchObject({ trigger: "/", query: "mo", start: 0 });
    expect(detectComposerTrigger("ask $rev", 8, items)).toMatchObject({ trigger: "$", query: "rev", start: 4 });
    expect(detectComposerTrigger("ask$rev", 7, items)).toBeNull();
  });

  it("opens on a slash at the start of a line, with everything on offer", () => {
    expect(at("/|")).toEqual({ trigger: "/", query: "", start: 0, end: 1, leading: true });
    expect(at("first line\n/re|")).toMatchObject({ query: "re", start: 11, leading: true });
    expect(at("  /re|")).toMatchObject({ query: "re", start: 2, leading: true });
    expect(ids("/|")).toEqual(["model", "compact", "review", "bro"]);
    // A description is searched here, and only here.
    expect(ids("/diff|")).toEqual(["review"]);
  });

  it("opens mid-sentence after a space, on the token alone", () => {
    expect(at("do the thing then /rev|")).toEqual({
      trigger: "/",
      query: "rev",
      start: 18,
      end: 22,
      leading: false,
    });
    expect(at("do the thing then /|")).toMatchObject({ query: "", start: 18, leading: false });
    // Once the token is finished the cursor is in another one.
    expect(at("do the thing then /review now|")).toBeNull();
  });

  it("offers mid-sentence only what the harness acts on there", () => {
    expect(ids("then /|")).toEqual(["review"]);
    expect(ids("then /rev|")).toEqual(["review"]);
    // Actions run on a thread, and a skill the model is not told about does
    // nothing unless it leads the prompt.
    expect(ids("then /comp|")).toEqual([]);
    expect(ids("then /bro|")).toEqual([]);
    // Prose that happens to start with a slash matches on names only.
    expect(ids("look in /diff|")).toEqual([]);
    expect(ids("then /rvw|")).toEqual([]);
  });

  it("does not open mid-sentence for a harness that ignores commands there", () => {
    const actionsOnly = slash.filter((item) => !item.inline);
    expect(at("then /re|", actionsOnly)).toBeNull();
    expect(at("/re|", actionsOnly)).toMatchObject({ query: "re", leading: true });
  });

  it("leaves paths and URLs alone", () => {
    expect(at("open src/foo|")).toBeNull();
    expect(at("open src/foo/bar|")).toBeNull();
    expect(at("a/|")).toBeNull();
    expect(at("see https://x/y|")).toBeNull();
    expect(at("see https://|")).toBeNull();
    expect(at("cd /usr/bin|")).toBeNull();
    expect(at("/usr/bin|")).toBeNull();
  });

  it("takes the whole token when the cursor is in the middle of it", () => {
    const text = "do /rev|iew now";
    const trigger = at(text)!;
    expect(trigger).toEqual({ trigger: "/", query: "rev", start: 3, end: 10, leading: false });
    expect(replaceComposerTrigger(text.replace("|", ""), trigger, "/review ").value).toBe(
      "do /review  now",
    );
    // Inside a path the token does not start with the slash.
    expect(at("open src/fo|o")).toBeNull();
    expect(at("cd /usr|/bin")).toMatchObject({ query: "usr", end: 11 });
  });

  it("replaces only the mid-sentence token it completed", () => {
    const text = "do the thing then /rev";
    const trigger = detectComposerTrigger(text, text.length, slash)!;
    expect(replaceComposerTrigger(text, trigger, "/review ")).toEqual({
      value: "do the thing then /review ",
      cursor: 26,
    });
  });

  it("ranks names and replaces only the active token", () => {
    const trigger = detectComposerTrigger("please $rev later", 11, items)!;
    expect(rankComposerItems(items, trigger)[0]?.id).toBe("review");
    expect(replaceComposerTrigger("please $rev later", trigger, "$review-code ")).toEqual({
      value: "please $review-code  later",
      cursor: 20,
    });
  });

  it("replaces the whole token when the caret is in its middle", () => {
    const trigger = detectComposerTrigger("ask $rev|iew later".replace("|", ""), 8, items)!;
    expect(replaceComposerTrigger("ask $review later", trigger, "$review-code ").value).toBe(
      "ask $review-code  later",
    );
    const slash = detectComposerTrigger("/model", 3, items)!;
    expect(replaceComposerTrigger("/model", slash, "").value).toBe("");
  });

  it("intercepts standalone actions but not prompt entries", () => {
    expect(submittedComposerAction(" /model ", items)?.item.id).toBe("model");
    expect(submittedComposerAction("/model\tignored", items)?.args).toBe("ignored");
    expect(submittedComposerAction("$review-code", items)).toBeNull();
    // An action named mid-prompt is part of the prompt.
    expect(submittedComposerAction("then /model", items)).toBeNull();
  });
});
