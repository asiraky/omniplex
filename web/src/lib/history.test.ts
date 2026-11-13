// @vitest-environment jsdom
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { createElement, StrictMode, useState } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { viewport } from "~/test/harness";
import { isDraftEntry, navigate, onMove, useBackDismiss } from "./history";

// jsdom delivers popstate a task after history.go, as a browser does, and
// under a loaded test run that task can come late: so a Back waits for its
// popstate, and an unwind (which the app never hears) for the depth it lands
// on. settle only lets React finish what was set off.
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 10));
  });
}

async function back() {
  await act(async () => {
    const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
    window.history.back();
    await popped;
  });
  await settle();
}

async function landsAt(depth: number) {
  await waitFor(() => expect(layerDepth()).toBe(depth));
  await settle();
}

const layerDepth = () => (window.history.state as { omniplexLayer?: number } | null)?.omniplexLayer ?? 0;

// Open state lives outside React so a test can close a sheet "by its own UI"
// the way a button would: by setting its state, not through history.
type Handle = { open: boolean; set: (open: boolean) => void };

function Sheet({ handle, initial = true }: { handle: Handle; initial?: boolean }) {
  const [open, setOpen] = useState(initial);
  handle.open = open;
  handle.set = setOpen;
  useBackDismiss(open, () => setOpen(false));
  return null;
}

function sheet(): Handle {
  return { open: false, set: () => {} };
}

function mount(...handles: Handle[]) {
  return render(createElement("div", null, ...handles.map((h, i) => createElement(Sheet, { key: i, handle: h }))));
}

beforeEach(() => {
  viewport("phone");
});

afterEach(async () => {
  cleanup();
  await landsAt(0);
  navigate("/");
});

describe("Back with a sheet open", () => {
  it("closes the sheet and leaves the URL alone", async () => {
    navigate("/thread/a");
    const a = sheet();
    mount(a);
    expect(a.open).toBe(true);

    await back();

    expect(a.open).toBe(false);
    expect(window.location.pathname).toBe("/thread/a");
    expect(layerDepth()).toBe(0);
  });

  it("closes nested sheets top first, one Back at a time", async () => {
    navigate("/thread/a");
    const lower = sheet();
    const upper = sheet();
    mount(lower, upper);

    await back();
    expect(upper.open).toBe(false);
    expect(lower.open).toBe(true);

    await back();
    expect(lower.open).toBe(false);
    expect(window.location.pathname).toBe("/thread/a");
  });
});

describe("a sheet closed by its own UI", () => {
  it("takes its entry with it, so Back goes to the previous thread", async () => {
    navigate("/thread/a");
    navigate("/thread/b");
    const a = sheet();
    mount(a);

    act(() => a.set(false));
    await landsAt(0);
    expect(window.location.pathname).toBe("/thread/b");

    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });

  it("unwinds two sheets closing together in one step", async () => {
    navigate("/thread/a");
    navigate("/thread/b");
    const lower = sheet();
    const upper = sheet();
    mount(lower, upper);
    expect(layerDepth()).toBe(2);

    act(() => {
      lower.set(false);
      upper.set(false);
    });
    await landsAt(0);
    expect(window.location.pathname).toBe("/thread/b");

    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });

  it("does not take a sheet that opens straight after it down too", async () => {
    // The palette closes, and a frame later the model picker opens: the
    // palette's unwind is still in flight when the picker's entry goes in.
    navigate("/thread/a");
    navigate("/thread/b");
    const first = sheet();
    const second = sheet();
    render(
      createElement(
        "div",
        null,
        createElement(Sheet, { handle: first }),
        createElement(Sheet, { handle: second, initial: false }),
      ),
    );

    act(() => first.set(false));
    await act(async () => {}); // the unwind's microtask, not its popstate
    act(() => second.set(true));
    await landsAt(1);
    expect(second.open).toBe(true);

    await back();
    expect(second.open).toBe(false);
    expect(window.location.pathname).toBe("/thread/b");
    expect(layerDepth()).toBe(0);

    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });
});

describe("navigate", () => {
  it("takes over a sheet's entry, so Back from the new thread lands on the old one", async () => {
    navigate("/thread/a");
    const a = sheet();
    mount(a);

    // Choosing a thread in the sheet: the app moves on, then the sheet closes.
    act(() => {
      navigate("/thread/b");
      a.set(false);
    });
    await settle();
    expect(window.location.pathname).toBe("/thread/b");

    await back();
    expect(window.location.pathname).toBe("/thread/a");
    expect(layerDepth()).toBe(0);
  });

  it("from nested sheets, leaves no dead step behind the new thread", async () => {
    navigate("/thread/a");
    const lower = sheet();
    const upper = sheet();
    mount(lower, upper);
    expect(layerDepth()).toBe(2);

    act(() => {
      navigate("/", { draft: true });
      lower.set(false);
      upper.set(false);
    });
    await waitFor(() => expect(isDraftEntry()).toBe(true));
    await settle();
    expect(layerDepth()).toBe(0);

    await back();
    expect(window.location.pathname).toBe("/thread/a");
    expect(layerDepth()).toBe(0);
  });

  it("waits for a sheet's unwind, which the app never hears", async () => {
    // Listening from before any sheet opens, as the app does from mount.
    const heard: string[] = [];
    const stop = onMove(() => heard.push(window.location.pathname));
    try {
      navigate("/thread/a");
      const a = sheet();
      mount(a);
      act(() => a.set(false));
      await act(async () => {}); // the unwind is in flight
      navigate("/thread/b");
      await waitFor(() => expect(window.location.pathname).toBe("/thread/b"));
      await settle();
      expect(heard).toEqual([]);

      await back();
      expect(window.location.pathname).toBe("/thread/a");
      expect(heard).toEqual(["/thread/a"]);
    } finally {
      stop();
    }
  });

  it("replaces a draft's entry", async () => {
    navigate("/thread/a");
    navigate("/", { draft: true });
    expect(isDraftEntry()).toBe(true);

    navigate("/thread/b");
    expect(isDraftEntry()).toBe(false);

    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });

  it("pushes from a thread's entry", async () => {
    navigate("/thread/a");
    navigate("/thread/b");

    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });
});

describe("useBackDismiss", () => {
  it("pushes nothing on a desktop", () => {
    viewport("desktop");
    navigate("/thread/a");
    const before = window.history.length;
    const a = sheet();
    mount(a);

    expect(window.history.length).toBe(before);
    expect(layerDepth()).toBe(0);
  });

  it("leaves one entry under StrictMode's double mount", async () => {
    navigate("/thread/a");
    navigate("/thread/b");
    const before = window.history.length;
    const a = sheet();
    render(createElement(StrictMode, null, createElement(Sheet, { handle: a })));
    await settle();

    expect(window.history.length).toBe(before + 1);
    expect(layerDepth()).toBe(1);

    await back();
    expect(a.open).toBe(false);
    expect(window.location.pathname).toBe("/thread/b");
    await back();
    expect(window.location.pathname).toBe("/thread/a");
  });
});
