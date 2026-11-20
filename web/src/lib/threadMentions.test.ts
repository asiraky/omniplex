import { describe, expect, it } from "vitest";

import type { ThreadMeta } from "~/protocol";
import { detectComposerTrigger } from "./composerItems";
import {
  MENTION_LIMIT,
  MENTION_TRIGGER,
  mentionCandidates,
  mentionCut,
  mentionItems,
  mentionedThread,
  splitMentions,
  withMentions,
} from "./threadMentions";

const t = (id: string, over: Partial<ThreadMeta> = {}) =>
  ({ id, title: id, projectId: "p1", createdAt: 0, updatedAt: 0, ...over }) as ThreadMeta;

describe("withMentions / splitMentions", () => {
  it("leaves text alone with nothing mentioned", () => {
    expect(withMentions("hi", [])).toBe("hi");
    expect(splitMentions("hi")).toEqual({ parts: [{ kind: "text", text: "hi" }], mentions: [] });
    expect(splitMentions("")).toEqual({ parts: [], mentions: [] });
  });

  it("round-trips mentions appended after the text", () => {
    const mentions = [
      { id: "t1", title: "Plan" },
      { id: "t2", title: "Fix" },
    ];
    const sent = withMentions("look at these", mentions);
    expect(sent.split("\n").slice(2)).toHaveLength(2);
    expect(splitMentions(sent)).toEqual({ parts: [{ kind: "text", text: "look at these" }], mentions });
  });

  it("round-trips a mention sent with no text", () => {
    const sent = withMentions("", [{ id: "t1", title: "Plan" }]);
    expect(splitMentions(sent)).toEqual({ parts: [], mentions: [{ id: "t1", title: "Plan" }] });
  });

  it("round-trips titles with brackets, parentheses and backslashes", () => {
    const m = { id: "t1", title: "a [b] (c) \\" };
    expect(splitMentions(withMentions("x", [m])).mentions).toEqual([m]);
  });

  it("keeps a link among the words in place rather than treating it as a mention", () => {
    const text = "see [@Plan](omniplex-thread://t1) then reply";
    expect(splitMentions(text)).toEqual({
      parts: [
        { kind: "text", text: "see " },
        { kind: "thread", id: "t1", title: "Plan" },
        { kind: "text", text: " then reply" },
      ],
      mentions: [],
    });
  });

  it("ignores ordinary links", () => {
    const text = "[@x](https://example.com)";
    expect(splitMentions(text).mentions).toEqual([]);
  });
});

describe("mentionCandidates", () => {
  it("offers the other threads in the same project", () => {
    const cur = t("me");
    const list = [cur, t("a"), t("b", { projectId: "p2" }), t("c", { projectId: undefined })];
    expect(mentionCandidates(list, cur).map((x) => x.id)).toEqual(["a"]);
  });

  it("offers nothing from a chat outside any project", () => {
    const cur = t("me", { projectId: undefined });
    expect(mentionCandidates([cur, t("c", { projectId: undefined })], cur)).toEqual([]);
  });
});

describe("mentionCut", () => {
  it("swallows the space after a token that already has one before it", () => {
    const text = "look at @plan please";
    const cut = mentionCut(text, 8, 13);
    expect(text.slice(0, cut.start) + text.slice(cut.end)).toBe("look at please");
  });

  it("keeps the space when the token starts the draft mid-word", () => {
    expect(mentionCut("x@plan y", 1, 6)).toEqual({ start: 1, end: 6 });
    expect(mentionCut("@plan y", 0, 5)).toEqual({ start: 0, end: 6 });
    expect(mentionCut("a @plan", 2, 7)).toEqual({ start: 2, end: 7 });
  });
});

describe("@ trigger", () => {
  it("detects @ mid-prompt and yields thread items for the query", () => {
    const trigger = detectComposerTrigger("ask @pla", 8, [MENTION_TRIGGER]);
    expect(trigger).toMatchObject({ trigger: "@", query: "pla", leading: false });
    const items = mentionItems([t("other"), t("planner")], trigger!.query);
    expect(items.map((i) => i.name)).toEqual(["planner"]);
    expect(mentionedThread(items[0])).toBe("planner");
  });

  it("caps the menu", () => {
    const many = Array.from({ length: MENTION_LIMIT + 5 }, (_, i) => t(`t${i}`));
    expect(mentionItems(many, "")).toHaveLength(MENTION_LIMIT);
  });

  it("does not take other entries for threads", () => {
    expect(mentionedThread({ ...MENTION_TRIGGER, action: "send-now" })).toBeNull();
  });
});
