import { describe, expect, it } from "vitest";

import { answerOf, initialDraft, pickOption, pickOther, type FieldDraft } from "./elicitationDraft";

const single = { type: "string", enum: ["a", "b", "c"], "x-allowOther": true };
const multi = { ...single, "x-multiSelect": true };

function draft(over: Partial<FieldDraft> = {}): FieldDraft {
  return { value: "", picked: [], otherOn: false, otherText: "", ...over };
}

describe("initialDraft", () => {
  it("seeds each field from its default, starting on the first step", () => {
    const d = initialDraft({
      one: { ...single, default: "b" },
      many: { ...multi, default: ["a", 3, "c"] },
      badMany: { ...multi, default: "a" },
      text: { type: "string", default: "hi" },
      blank: { type: "string" },
    });
    expect(d.step).toBe(0);
    expect(d.fields.one.picked).toEqual(["b"]);
    expect(d.fields.many.picked).toEqual(["a", "c"]);
    expect(d.fields.badMany.picked).toEqual([]);
    expect(d.fields.text).toEqual(draft({ value: "hi" }));
    expect(d.fields.blank.value).toBe("");
  });
});

describe("pickOption", () => {
  it("replaces a single-select's pick and drops Other", () => {
    const d = pickOption(single, draft({ picked: ["a"], otherOn: true, otherText: "x" }), "b");
    expect(d).toEqual(draft({ picked: ["b"], otherOn: false, otherText: "x" }));
  });

  it("toggles a multi-select's pick, keeping order and Other", () => {
    let d = draft({ otherOn: true });
    d = pickOption(multi, d, "c");
    d = pickOption(multi, d, "a");
    expect(d.picked).toEqual(["c", "a"]);
    d = pickOption(multi, d, "c");
    expect(d).toEqual(draft({ picked: ["a"], otherOn: true }));
  });
});

describe("pickOther", () => {
  it("gives up a single-select's pick for Other", () => {
    expect(pickOther(single, draft({ picked: ["a"] }))).toEqual(draft({ otherOn: true }));
  });

  it("toggles a multi-select's Other, keeping its picks", () => {
    const on = pickOther(multi, draft({ picked: ["a"] }));
    expect(on).toEqual(draft({ picked: ["a"], otherOn: true }));
    expect(pickOther(multi, on)).toEqual(draft({ picked: ["a"] }));
  });
});

describe("answerOf", () => {
  it.each([
    ["single, nothing picked", single, draft(), ""],
    ["single, option", single, draft({ picked: ["b"] }), "b"],
    ["single, Other trimmed", single, draft({ otherOn: true, otherText: "  mine " }), "mine"],
    ["single, Other off keeps typed text out", single, draft({ picked: ["a"], otherText: "x" }), "a"],
    ["multi, Other last", multi, draft({ picked: ["c", "a"], otherOn: true, otherText: " z " }), ["c", "a", "z"]],
    ["multi, blank Other dropped", multi, draft({ picked: ["a"], otherOn: true, otherText: "  " }), ["a"]],
    ["plain field", { type: "number" }, draft({ value: 4 }), 4],
  ])("%s", (_, field, d, expected) => {
    expect(answerOf(field, d)).toEqual(expected);
  });
});
