// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { QueuedPrompt } from "~/protocol";
import { render, wrap } from "~/test/harness";

import { QueuedPrompts } from "./QueuedPrompts";

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

const first: QueuedPrompt = { queueId: "q1", prompt: "do the thing" };
const second: QueuedPrompt = { queueId: "q2", prompt: "then this" };

function mount(queued: QueuedPrompt[]) {
  const onEditLost = vi.fn();
  const ui = (q: QueuedPrompt[]) => (
    <QueuedPrompts queued={q} threadId="t" artefacts={[]} busy steers onEditLost={onEditLost} />
  );
  const { rerender, unmount } = render(ui(queued));
  return { onEditLost, rerender: (q: QueuedPrompt[]) => rerender(wrap(ui(q))), unmount };
}

// The cards load lazily, so the first query waits for them.
const edit = async (name: string, value: string) => {
  fireEvent.click(await screen.findByRole("button", { name }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edit queued prompt" }), { target: { value } });
};

describe("a queued prompt that starts while it is being edited", () => {
  it("hands on what was typed once the prompt leaves the queue", async () => {
    const { onEditLost, rerender } = mount([first, second]);
    await edit("do the thing", "do the other thing\n");
    rerender([second]);
    expect(onEditLost).toHaveBeenCalledExactlyOnceWith("do the other thing");
    rerender([]);
    expect(onEditLost).toHaveBeenCalledTimes(1);
  });

  it("hands it on when the harness takes the prompt as it stands", async () => {
    const { onEditLost, rerender } = mount([first]);
    await edit("do the thing", "reworded");
    rerender([{ ...first, sent: true }]);
    expect(onEditLost).toHaveBeenCalledExactlyOnceWith("reworded");
  });

  it("has nothing to hand on when the words were not changed", async () => {
    const { onEditLost, rerender } = mount([first]);
    await edit("do the thing", "do the thing");
    rerender([]);
    expect(onEditLost).not.toHaveBeenCalled();
  });

  it("has nothing to hand on once the edit was cancelled", async () => {
    const { onEditLost, rerender } = mount([first]);
    await edit("do the thing", "reworded");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    rerender([]);
    expect(onEditLost).not.toHaveBeenCalled();
  });

  it("leaves other prompts' edits where they are", async () => {
    const { onEditLost, rerender } = mount([first, second]);
    await edit("then this", "and then this");
    rerender([second]);
    expect(onEditLost).not.toHaveBeenCalled();
  });

  it("drops the edit when the transcript goes away instead", async () => {
    const { onEditLost, unmount } = mount([first]);
    await edit("do the thing", "reworded");
    unmount();
    expect(onEditLost).not.toHaveBeenCalled();
  });
});
