// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { Item } from "~/protocol";

import { NoticeCard } from "./NoticeCard";

afterEach(cleanup);

// The catalogue knows "opus" and "claude-opus-5" are one model.
const catalogue = (model: string, effort: string) =>
  `${model === "claude-opus-5" ? "opus" : model}${effort ? ` · ${effort}` : ""}`;

const notice = (over: Partial<Item>): Item => ({ id: "model:4", kind: "notice", noticeKind: "model", ...over });

describe("NoticeCard model switch", () => {
  it("names the settings the thread switched to", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "sonnet", effort: "high", from: { model: "opus", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("Switched to sonnet · high");
  });

  it("draws nothing when both sides name the same model under different ids", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "opus", effort: "high", from: { model: "claude-opus-5", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("");
  });

  it("draws nothing until the catalogue has loaded", () => {
    const { container } = render(
      <NoticeCard item={notice({ model: "opus", effort: "high", from: { model: "claude-opus-5", effort: "high" } })} />,
    );
    expect(container.textContent).toBe("");
  });

  it("still marks an effort change on the same model", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "opus", effort: "low", from: { model: "claude-opus-5", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("Switched to opus · low");
  });
});
