// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ReadAloudContext } from "~/lib/readAloud";
import { SPEAK_ROOT_ATTR } from "~/lib/speech";
import { player } from "~/lib/ttsPlayer";
import type { Item } from "~/protocol";
import { render } from "~/test/harness";

import { MessageMeta } from "./MessageMeta";
import { UserMessage } from "./UserMessage";

function Msg({ item }: { item: Item }) {
  const ref = useRef<HTMLDivElement>(null);
  return (
    <div>
      <div ref={ref} {...{ [SPEAK_ROOT_ATTR]: "" }}>
        <p>{item.text}</p>
      </div>
      <MessageMeta item={item} speakRef={ref} />
    </div>
  );
}

const items: Item[] = [
  { id: "m1", kind: "message", role: "agent", text: "First answer here." },
  { id: "m2", kind: "message", role: "agent", text: "Second answer here." },
];

function transcript(on = true) {
  render(
    <ReadAloudContext.Provider value={on ? { threadId: "t1", title: "A thread" } : null}>
      {items.map((item) => (
        <Msg key={item.id} item={item} />
      ))}
    </ReadAloudContext.Provider>,
  );
}

function select(from: string, fromOffset: number, to: string, toOffset: number) {
  const node = (text: string) => screen.getByText(text).firstChild!;
  const r = document.createRange();
  r.setStart(node(from), fromOffset);
  r.setEnd(node(to), toOffset);
  act(() => {
    const sel = document.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(r);
    document.dispatchEvent(new Event("selectionchange"));
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  document.getSelection()?.removeAllRanges();
});

describe("the play button", () => {
  it("is not there without read-aloud", () => {
    transcript(false);
    expect(screen.queryByRole("button", { name: "Read aloud" })).toBeNull();
  });

  it("reads the whole message", () => {
    const play = vi.spyOn(player, "play").mockImplementation(() => {});
    transcript();
    fireEvent.click(screen.getAllByRole("button", { name: "Read aloud" })[1]);
    expect(play).toHaveBeenCalledWith("m2", "Second answer here.", "t1", "A thread");
  });

  it("reads a prompt's markdown the way a reply's is read, though it is shown raw", () => {
    const play = vi.spyOn(player, "play").mockImplementation(() => {});
    const item: Item = {
      id: "u1",
      kind: "message",
      role: "user",
      text: "Why does **this** fail?\n\n```go\npanic(err)\n```\n\n- `internal/tts/cache.go`",
    };
    render(
      <ReadAloudContext.Provider value={{ threadId: "t1", title: "A thread" }}>
        <UserMessage item={item} threadId="t1" artefacts={[]} />
      </ReadAloudContext.Provider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Read aloud" }));
    expect(play).toHaveBeenCalledWith(
      "u1",
      "Why does this fail?\nCode snippet.\ncache.go.",
      "t1",
      "A thread",
    );

    const bubble = screen.getByText(/Why does/).firstChild!;
    const r = document.createRange();
    r.setStart(bubble, 0);
    r.setEnd(bubble, item.text!.indexOf("panic"));
    act(() => {
      document.getSelection()!.addRange(r);
      document.dispatchEvent(new Event("selectionchange"));
    });
    fireEvent.click(screen.getByRole("button", { name: "Read the selection aloud" }));
    expect(play).toHaveBeenLastCalledWith("u1", "Why does this fail?\nCode snippet.", "t1", "A thread");
  });

  it("reads a selection that spans messages from either message's button", () => {
    const play = vi.spyOn(player, "play").mockImplementation(() => {});
    transcript();
    select("First answer here.", 6, "Second answer here.", 6);
    const buttons = screen.getAllByRole("button", { name: "Read the selection aloud" });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    expect(play.mock.calls[0][1]).toMatch(/^answer here\.\s+Second\.?$/);
  });

  it("only offers the selection to the messages it reaches", () => {
    transcript();
    select("Second answer here.", 0, "Second answer here.", 6);
    expect(screen.getAllByRole("button", { name: "Read aloud" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Read the selection aloud" })).toHaveLength(1);
  });

  it("reads what was selected when the tap began, though the tap cleared it", () => {
    const play = vi.spyOn(player, "play").mockImplementation(() => {});
    transcript();
    select("First answer here.", 0, "First answer here.", 5);
    const button = screen.getByRole("button", { name: "Read the selection aloud" });
    fireEvent.pointerDown(button);
    // A phone dropping the selection between finger down and click.
    select("First answer here.", 0, "First answer here.", 0);
    fireEvent.click(button);
    expect(play.mock.calls[0][1]).toMatch(/^First\.?$/);
  });

  it("stops what is playing", () => {
    const stop = vi.spyOn(player, "stop").mockImplementation(() => {});
    vi.spyOn(player, "getState").mockReturnValue({ playingKey: "m1", status: "playing" });
    transcript();
    fireEvent.click(screen.getByRole("button", { name: "Stop reading aloud" }));
    expect(stop).toHaveBeenCalled();
  });
});
