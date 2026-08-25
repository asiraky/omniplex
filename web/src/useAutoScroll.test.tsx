// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAutoScroll } from "./useAutoScroll";

// jsdom does no layout, so the scroller's geometry is stated outright: a
// 1000px column in a 400px window, which is exactly the arithmetic the hook
// does and nothing more.
const CONTENT = 1000;
const VIEWPORT = 400;
const BOTTOM = CONTENT - VIEWPORT;

let stick: () => void;
let scrollToBottom: () => void;
let anchorTo: (el: HTMLElement | null) => void;
// The content ResizeObserver's callback, so a growing transcript can be played
// out a step at a time — jsdom has no layout to grow on its own.
let grew: () => void;

function Harness() {
  const auto = useAutoScroll<HTMLDivElement, HTMLDivElement>();
  stick = auto.stick;
  scrollToBottom = auto.scrollToBottom;
  anchorTo = auto.anchorTo;
  return (
    <>
      <div ref={auto.scrollerRef} data-testid="scroller">
        <div ref={auto.contentRef} data-testid="content">
          <div data-testid="prompt" />
        </div>
      </div>
      <span data-testid="pinned">{String(auto.pinned)}</span>
    </>
  );
}

function scroller() {
  return screen.getByTestId("scroller");
}

// What the hook is asking the content's padding to add, in px.
function reserve() {
  return parseInt(screen.getByTestId("content").style.getPropertyValue("--anchor-reserve") || "0", 10);
}

// Place the prompt element `offset` px into the transcript and hand it to the
// hook, the way the transcript does when a prompt of its own arrives. This is
// a layout position, not a painted one, so unlike a rect it does not move with
// the scroll position — the hook measures with `offsetTop` precisely so that a
// half-played fade-in cannot shift it.
function layoutAt(el: HTMLElement, top: number) {
  Object.defineProperty(el, "offsetTop", { configurable: true, value: top });
  Object.defineProperty(el, "offsetParent", { configurable: true, value: null });
}

function anchorPrompt(offset: number) {
  const target = screen.getByTestId("prompt");
  layoutAt(target, offset);
  layoutAt(scroller(), 0);
  act(() => anchorTo(target));
}

function pinned() {
  return screen.getByTestId("pinned").textContent === "true";
}

function setTop(top: number) {
  scroller().scrollTop = top;
}

function fire(event: Event) {
  act(() => {
    scroller().dispatchEvent(event);
  });
}

function settle(fn: () => void) {
  act(fn);
}

function scrollTo(top: number) {
  setTop(top);
  fire(new Event("scroll"));
}

function wheel(deltaY: number) {
  fire(new WheelEvent("wheel", { deltaY }));
}

// The transcript's own height, before any room the hook reserves under it —
// which is real height too, so the scroller reports the sum.
let content = CONTENT;

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: () => void) {
        grew = () => act(cb);
      }
      observe() {}
      disconnect() {}
    },
  );
  content = CONTENT;
  render(<Harness />);
  const el = scroller();
  Object.defineProperty(el, "scrollHeight", { get: () => content + reserve(), configurable: true });
  Object.defineProperty(el, "clientHeight", { value: VIEWPORT, configurable: true });
  // A real scroller clamps: the position cannot pass the last screenful, so
  // "scroll to scrollHeight" lands at BOTTOM and stays there.
  let top = BOTTOM;
  Object.defineProperty(el, "scrollTop", {
    get: () => top,
    set: (v: number) => {
      top = Math.max(0, Math.min(v, el.scrollHeight - VIEWPORT));
    },
    configurable: true,
  });
  el.scrollTo = vi.fn();
  // The mount snap has already happened by now in a browser; this is the
  // scroll event it would have produced.
  fire(new Event("scroll"));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("useAutoScroll", () => {
  it("starts pinned", () => {
    expect(pinned()).toBe(true);
  });

  it("follows content that grows underneath it while pinned", () => {
    Object.defineProperty(scroller(), "scrollHeight", { value: CONTENT + 400, configurable: true });
    settle(stick);
    expect(pinned()).toBe(true);
    expect(scroller().scrollTop).toBe(CONTENT + 400 - VIEWPORT);
  });

  it("unpins on a wheel scroll upwards", () => {
    wheel(-40);
    expect(pinned()).toBe(false);
  });

  it("ignores a wheel scroll downwards", () => {
    wheel(40);
    expect(pinned()).toBe(true);
  });

  it("unpins when the position drops with no event of its own", () => {
    // A scrollbar drag: the position moves and nothing else says so.
    scrollTo(BOTTOM - 200);
    expect(pinned()).toBe(false);
  });

  it("leaves the view alone once unpinned", () => {
    scrollTo(BOTTOM - 200);
    settle(stick);
    expect(scroller().scrollTop).toBe(BOTTOM - 200);
  });

  it("stays unpinned after a nudge too small to leave the bottom tolerance", () => {
    wheel(-5);
    setTop(BOTTOM - 5);
    fire(new Event("scroll"));
    expect(pinned()).toBe(false);
  });

  it("unpins when content is dragged away underneath the pin", () => {
    // A scrollbar drag during a stream: the position moves, and content
    // arrives before the scroll event does.
    setTop(BOTTOM - 200);
    settle(stick);
    expect(pinned()).toBe(false);
    expect(scroller().scrollTop).toBe(BOTTOM - 200);
  });

  it("stays pinned when shrinking content clamps the position", () => {
    // A tool card closing: the view has nowhere to be but the new bottom.
    Object.defineProperty(scroller(), "scrollHeight", { value: 500, configurable: true });
    setTop(100);
    fire(new Event("scroll"));
    settle(stick);
    expect(pinned()).toBe(true);
  });

  it("ignores intent on a transcript that does not scroll", () => {
    Object.defineProperty(scroller(), "scrollHeight", { value: VIEWPORT, configurable: true });
    wheel(-40);
    fire(new KeyboardEvent("keydown", { key: "PageUp" }));
    expect(pinned()).toBe(true);
  });

  it("re-pins when the reader scrolls back to the bottom", () => {
    scrollTo(BOTTOM - 200);
    scrollTo(BOTTOM);
    expect(pinned()).toBe(true);
  });

  it("re-pins and animates when the button is used", () => {
    scrollTo(BOTTOM - 200);
    settle(scrollToBottom);
    expect(pinned()).toBe(true);
    expect(scroller().scrollTo).toHaveBeenCalledWith({ top: CONTENT, behavior: "smooth" });
  });

  it("jumps instead of animating under reduced motion", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    scrollTo(BOTTOM - 200);
    settle(scrollToBottom);
    expect(scroller().scrollTo).not.toHaveBeenCalled();
    expect(scroller().scrollTop).toBe(BOTTOM);
  });

  it("unpins on a touch drag downwards", () => {
    const touch = (clientY: number) => [{ clientY } as Touch];
    fire(new TouchEvent("touchstart", { touches: touch(200) }));
    fire(new TouchEvent("touchmove", { touches: touch(260) }));
    expect(pinned()).toBe(false);
  });

  it("unpins on a key that scrolls upwards", () => {
    fire(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true }));
    expect(pinned()).toBe(false);
  });
});

// Sending a prompt asks the view for something the pin cannot give: the newest
// message at the *top*, with the answer streaming into the space below it. The
// space has to be invented — nothing follows the prompt yet — and then given
// back as the answer takes its place.
// Where the prompt sits in the transcript: 300px down a view scrolled to 600.
const PROMPT_AT = 900;
const ANCHORED = PROMPT_AT - 16;

describe("useAutoScroll anchoring", () => {
  it("lifts the anchored element to the top of the view", () => {
    anchorPrompt(PROMPT_AT);
    // The element lands a hair below the top edge, and the room to put it
    // there — a viewport's worth, less what already followed it — is reserved.
    expect(scroller().scrollTop).toBe(ANCHORED);
    expect(reserve()).toBe(VIEWPORT - (CONTENT - ANCHORED));
    expect(pinned()).toBe(false);
  });

  it("holds it there while the answer streams in underneath", () => {
    anchorPrompt(PROMPT_AT);
    content += 100;
    grew();
    expect(scroller().scrollTop).toBe(ANCHORED);
    expect(reserve()).toBe(VIEWPORT - (CONTENT + 100 - ANCHORED));
  });

  it("hands the view back to the pin once the answer fills it on its own", () => {
    anchorPrompt(PROMPT_AT);
    content += 1000;
    grew();
    expect(reserve()).toBe(0);
    expect(pinned()).toBe(true);
    expect(scroller().scrollTop).toBe(content - VIEWPORT);
  });

  it("lets the reader take the view back from the anchor", () => {
    anchorPrompt(PROMPT_AT);
    scrollTo(120);
    content += 100;
    grew();
    expect(scroller().scrollTop).toBe(120);
    expect(pinned()).toBe(false);
  });

  it("gives the reserved room back as the transcript grows into it", () => {
    anchorPrompt(PROMPT_AT);
    wheel(-40);
    content += 1000;
    grew();
    expect(reserve()).toBe(0);
  });

  it("keeps the hold when content above it collapses", () => {
    anchorPrompt(PROMPT_AT);
    // A card above the prompt folds away: the prompt moves up the content and
    // the browser clamps the view to the shorter bottom on its own. That
    // scroll event is not a reader taking the view back, and treating it as
    // one dropped the hold and left the reserve behind with nothing holding
    // it up — so the tail was never handed back either.
    content -= 100;
    layoutAt(screen.getByTestId("prompt"), PROMPT_AT - 100);
    setTop(scroller().scrollTop);
    fire(new Event("scroll"));

    content += 1000;
    grew();
    expect(pinned()).toBe(true);
    expect(scroller().scrollTop).toBe(content - VIEWPORT);
  });

  it("still lets the reader scroll down out of the anchor", () => {
    anchorPrompt(PROMPT_AT);
    // The turn grew before the observer got a look in, so there is now room
    // below the anchored position — and the reader went down into it. Ending
    // at the bottom does not make that the browser: a clamp can only ever
    // move the view up.
    content += 50;
    scrollTo(content + reserve() - VIEWPORT);

    // The hold is gone, so the tail is theirs again — which is exactly what
    // holding on through a downward move would have denied them.
    expect(pinned()).toBe(true);
  });

  it("does not let its own scroll re-arm the pin", () => {
    // The reserve is sized so the anchored position is the last screenful,
    // which means the scroll event the hook's own write produces arrives
    // looking exactly like the reader parking at the bottom.
    anchorPrompt(PROMPT_AT);
    fire(new Event("scroll"));
    expect(pinned()).toBe(false);
    settle(stick);
    expect(scroller().scrollTop).toBe(ANCHORED);
  });

  it("still lifts when the view was already full without any reserve", () => {
    // A tall prompt under a tall composer: everything below the anchor already
    // fills the view, so no room has to be made — but the lift is still owed.
    content = 2000;
    anchorPrompt(PROMPT_AT);
    expect(reserve()).toBe(0);
    expect(scroller().scrollTop).toBe(ANCHORED);
  });

  it("drops the anchor when the button is used", () => {
    anchorPrompt(PROMPT_AT);
    settle(scrollToBottom);
    expect(reserve()).toBe(0);
    expect(pinned()).toBe(true);
  });
});
