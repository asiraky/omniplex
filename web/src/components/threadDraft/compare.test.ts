import { describe, expect, it } from "vitest";

import type { ModelSelection } from "~/components/ModelPicker";

import { MAX_COMPARE, addCompare, compareBranches, compareLabels, compareTitle } from "./compare";

const pick = (model: string, instance = "claude"): ModelSelection => ({
  harness: "claude",
  instance,
  model,
});

describe("addCompare", () => {
  const primary = pick("opus");

  it("adds a model to the end of the set", () => {
    const set = [pick("sonnet")];
    expect(addCompare(set, primary, pick("haiku"))).toEqual([pick("sonnet"), pick("haiku")]);
    expect(set).toHaveLength(1);
  });

  it("refuses a model already in the set, returning the same array", () => {
    const set = [pick("sonnet")];
    expect(addCompare(set, primary, pick("sonnet"))).toBe(set);
  });

  it("refuses the draft's own model, returning the same array", () => {
    const set: ModelSelection[] = [];
    expect(addCompare(set, primary, pick("opus"))).toBe(set);
  });

  it("refuses once the set is full, returning the same array", () => {
    const set = Array.from({ length: MAX_COMPARE }, (_, i) => pick(`m${i}`));
    const got = addCompare(set, primary, pick("another"));
    expect(got).toBe(set);
    expect(got).toHaveLength(MAX_COMPARE);
  });

  it("allows filling the set up to the limit", () => {
    let set: ModelSelection[] = [];
    for (let i = 0; i < MAX_COMPARE + 2; i++) set = addCompare(set, primary, pick(`m${i}`));
    expect(set.map((m) => m.model)).toEqual(
      Array.from({ length: MAX_COMPARE }, (_, i) => `m${i}`),
    );
  });

  it("allows the same model under another account", () => {
    const set = [pick("sonnet")];
    expect(addCompare(set, primary, pick("sonnet", "claude-work"))).toEqual([
      pick("sonnet"),
      pick("sonnet", "claude-work"),
    ]);
    // The primary's model under a second account is a different thread too.
    expect(addCompare([], primary, pick("opus", "claude-work"))).toEqual([
      pick("opus", "claude-work"),
    ]);
  });
});

describe("compareBranches", () => {
  it("leaves every branch empty when none was typed", () => {
    expect(compareBranches("", ["a", "b"])).toEqual(["", ""]);
    expect(compareBranches("  \n\t ", ["a", "b", "c"])).toEqual(["", "", ""]);
  });

  it("puts each model's id on the end of a typed branch", () => {
    expect(compareBranches(" feat/x ", ["claude-opus-5", "gpt-5.6-sol"])).toEqual([
      "feat/x-claude-opus-5",
      "feat/x-gpt-5.6-sol",
    ]);
  });

  it("drops the 1M window tag", () => {
    expect(compareBranches("b", ["claude-opus-5[1m]"])).toEqual(["b-claude-opus-5"]);
  });

  it("slugs punctuation and case", () => {
    expect(compareBranches("b", ["Org/Model_Name:Latest", "..odd--name.."])).toEqual([
      "b-org-model-name-latest",
      "b-odd-name",
    ]);
  });

  it("falls back to a placeholder for a model with nothing sluggable", () => {
    expect(compareBranches("b", ["", "///"])).toEqual(["b-model", "b-model-2"]);
  });

  it("numbers the same model run twice so the branches differ", () => {
    expect(compareBranches("b", ["sonnet", "sonnet", "sonnet[1m]", "opus"])).toEqual([
      "b-sonnet",
      "b-sonnet-2",
      "b-sonnet-3",
      "b-opus",
    ]);
  });
});

describe("compareLabels", () => {
  it("leaves names that differ alone", () => {
    expect(
      compareLabels([
        { label: "Opus 5", account: "Claude" },
        { label: "Sonnet 5", account: "Claude" },
      ]),
    ).toEqual(["Opus 5", "Sonnet 5"]);
  });

  it("names the account for the same model on two accounts", () => {
    expect(
      compareLabels([
        { label: "Sonnet 5", account: "Claude" },
        { label: "Opus 5", account: "Claude" },
        { label: "Sonnet 5", account: "Work" },
      ]),
    ).toEqual(["Sonnet 5 (Claude)", "Opus 5", "Sonnet 5 (Work)"]);
  });

  it("keeps the bare name when there is no account to tell them apart by", () => {
    expect(
      compareLabels([
        { label: "Sonnet 5", account: "" },
        { label: "Sonnet 5", account: "Work" },
      ]),
    ).toEqual(["Sonnet 5", "Sonnet 5 (Work)"]);
  });
});

describe("compareTitle", () => {
  it("joins a short prompt and the model", () => {
    expect(compareTitle("Fix the bug", "Opus 5")).toBe("Fix the bug · Opus 5");
  });

  it("collapses whitespace and newlines", () => {
    expect(compareTitle("  Fix\n\n the \t bug \n", "Opus 5")).toBe("Fix the bug · Opus 5");
  });

  it("keeps a prompt of exactly the limit whole", () => {
    const prompt = "a".repeat(50);
    expect(compareTitle(prompt, "L")).toBe(`${prompt} · L`);
  });

  it("truncates a long prompt with an ellipsis", () => {
    expect(compareTitle("a".repeat(51), "L")).toBe(`${"a".repeat(50)}… · L`);
  });

  it("trims a trailing space left by the cut", () => {
    const prompt = `${"a".repeat(49)} bcd`;
    expect(compareTitle(prompt, "L")).toBe(`${"a".repeat(49)}… · L`);
  });

  it("counts runes, not UTF-16 units", () => {
    // 30 emoji are 60 code units but 30 runes: under the limit, kept whole.
    const short = "😀".repeat(30);
    expect(compareTitle(short, "L")).toBe(`${short} · L`);
    // 60 emoji cut at 50 runes, never through a surrogate pair.
    const got = compareTitle("😀".repeat(60), "L");
    expect(got).toBe(`${"😀".repeat(50)}… · L`);
    expect(got).not.toMatch(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])/);
  });

  it("is the label alone for an empty prompt", () => {
    expect(compareTitle("", "Opus 5")).toBe("Opus 5");
    expect(compareTitle(" \n ", "Opus 5")).toBe("Opus 5");
  });
});
