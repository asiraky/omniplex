// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { ReplyContext } from "~/lib/reply";
import type { Item } from "~/protocol";
import { render } from "~/test/harness";

import { MessageMeta } from "./MessageMeta";

function Msg({ item, replyable = true }: { item: Item; replyable?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  return (
    <div>
      <div ref={ref}>
        <p>{item.text}</p>
      </div>
      <MessageMeta item={item} speakRef={ref} replyable={replyable} />
    </div>
  );
}

const items: Item[] = [
  { id: "m1", kind: "message", role: "agent", text: "First **answer** here." },
  { id: "m2", kind: "message", role: "agent", text: "Second answer here." },
];

/** A mouse answers the hover query; a phone does not. */
function pointer(kind: "mouse" | "touch") {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query.includes("hover: hover") ? kind === "mouse" : false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
  }));
}

function transcript(onReply: ((quote: string) => void) | null, list = items) {
  render(
    <ReplyContext.Provider value={onReply}>
      {list.map((item) => (
        <Msg key={item.id} item={item} />
      ))}
    </ReplyContext.Provider>,
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

describe("the reply button", () => {
  it("is not there without a composer to write into", () => {
    transcript(null);
    expect(screen.queryByRole("button", { name: /^Reply/ })).toBeNull();
  });

  it("is not offered on a message that is not replyable", () => {
    render(
      <ReplyContext.Provider value={() => {}}>
        <Msg item={{ id: "u1", kind: "message", role: "user", text: "A prompt" }} replyable={false} />
      </ReplyContext.Provider>,
    );
    expect(screen.queryByRole("button", { name: /^Reply/ })).toBeNull();
  });

  it("quotes the whole message as written", () => {
    pointer("mouse");
    const reply = vi.fn();
    transcript(reply);
    fireEvent.click(screen.getAllByRole("button", { name: "Reply quoting this message" })[0]);
    expect(reply).toHaveBeenCalledWith("First **answer** here.");
  });

  it("with a mouse, quotes only this message's part of a selection and clears it", () => {
    pointer("mouse");
    const reply = vi.fn();
    transcript(reply);
    select("First **answer** here.", 6, "Second answer here.", 6);
    const buttons = screen.getAllByRole("button", { name: "Reply quoting the selection" });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[1]);
    expect(reply).toHaveBeenCalledWith("Second");
    expect(document.getSelection()!.isCollapsed).toBe(true);
    document.dispatchEvent(new Event("selectionchange"));
  });

  it("offers the selection only to the messages it reaches", () => {
    pointer("mouse");
    transcript(vi.fn());
    select("Second answer here.", 0, "Second answer here.", 6);
    expect(screen.getAllByRole("button", { name: "Reply quoting this message" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Reply quoting the selection" })).toHaveLength(1);
    act(() => document.getSelection()!.removeAllRanges());
  });

  it("on a phone, quotes the whole message even with text selected", () => {
    pointer("touch");
    const reply = vi.fn();
    transcript(reply);
    select("Second answer here.", 0, "Second answer here.", 6);
    const buttons = screen.getAllByRole("button", { name: "Reply quoting this message" });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[1]);
    expect(reply).toHaveBeenCalledWith("Second answer here.");
    act(() => document.getSelection()!.removeAllRanges());
  });
});
