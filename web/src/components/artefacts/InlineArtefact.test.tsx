// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ArtefactCards } from "~/components/transcript/ArtefactCards";
import type { Artefact } from "~/lib/artefacts";
import type { Item } from "~/protocol";
import { makeArtefact } from "~/test/artefact";
import { render } from "~/test/harness";

let modified = 0;
// A fresh revision per test: the frame remembers each revision's height.
function page(over: Partial<Artefact> = {}): Artefact {
  return makeArtefact({ name: "chart.html", mediaType: "text/html", inline: true, modifiedAt: ++modified, ...over });
}
const showing = (over: Partial<Item> = {}): Item => ({
  id: "artefact:a1@1",
  kind: "artefact",
  artefactId: "a1",
  inline: true,
  ...over,
});

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetchMock = vi.fn(async () => Response.json({ url: "/p/tok/chart.html", expiresAt: Date.now() + 3_600_000 }));
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

function mount(a = page(), item = showing(), onOpen = vi.fn()) {
  render(<ArtefactCards threadId="s1" items={[item]} artefacts={[a]} onOpen={onOpen} />);
  return onOpen;
}

const frame = () => document.querySelector("iframe");

function fromFrame(data: unknown, source: MessageEventSource | null = frame()!.contentWindow) {
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { data, source }));
  });
}

describe("an inline page in the transcript", () => {
  it("sizes the frame to the page, and offers Expand once the page is past the cap", async () => {
    const onOpen = mount();
    await waitFor(() => expect(frame()).not.toBeNull());
    expect(frame()!.getAttribute("src")).toBe("/p/tok/chart.html");

    fromFrame({ omniplex: "size", height: 300 });
    expect(frame()!.style.height).toBe("300px");
    expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();

    // The harness is a desktop, so the cap is 640px.
    fromFrame({ omniplex: "size", height: 2000 });
    expect(frame()!.style.height).toBe("640px");
    fireEvent.click(screen.getByRole("button", { name: "Expand" }));
    expect(onOpen).toHaveBeenCalledWith("a1");
  });

  it("ignores a size from anything but its own frame", async () => {
    mount();
    await waitFor(() => expect(frame()).not.toBeNull());
    fromFrame({ omniplex: "size", height: 2000 }, window);
    expect(frame()!.style.height).toBe("360px");
    expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
  });

  it("waits for a fresh token rather than loading the page on one that has lapsed", async () => {
    fetchMock
      .mockResolvedValueOnce(Response.json({ url: "/p/old/chart.html", expiresAt: Date.now() - 1 }))
      .mockResolvedValueOnce(Response.json({ url: "/p/new/chart.html", expiresAt: Date.now() + 3_600_000 }));
    mount();
    await waitFor(() => expect(frame()).not.toBeNull());
    expect(frame()!.getAttribute("src")).toBe("/p/new/chart.html");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("is a card when the artefact was shown again without inline", () => {
    mount(page({ inline: undefined }));
    expect(frame()).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /chart\.html/ })).toBeTruthy();
  });

  it("asks for the page only once it comes near the view, and lets go of the frame when it leaves", async () => {
    let report: (near: boolean) => void = () => {};
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        constructor(cb: IntersectionObserverCallback) {
          report = (near) => cb([{ isIntersecting: near } as IntersectionObserverEntry], this as never);
        }
        observe() {}
        disconnect() {}
      },
    );
    mount();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(frame()).toBeNull();

    act(() => report(true));
    await waitFor(() => expect(frame()).not.toBeNull());
    fromFrame({ omniplex: "size", height: 400 });

    act(() => report(false));
    expect(frame()).toBeNull();
    // The box keeps the page's height, so nothing around it moves.
    const box = screen.getByRole("button", { name: "Open chart.html" }).nextElementSibling as HTMLElement;
    expect(box.style.height).toBe("400px");

    act(() => report(true));
    await waitFor(() => expect(frame()).not.toBeNull());
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
