// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ArtefactSurface } from "~/components/artefacts/ArtefactSurface";
import { forgetTextCache, readText, TEXT_CAP } from "~/components/artefacts/viewers";
import { rawUrl, type Artefact, type ArtefactVersion } from "~/lib/artefacts";
import { render, viewport } from "~/test/harness";

function artefact(name: string, mediaType: string, versions = 1, over: Partial<ArtefactVersion> = {}): Artefact {
  return {
    id: `a-${name}`,
    name,
    versions: Array.from({ length: versions }, (_, i) => ({
      version: i + 1,
      mediaType,
      size: 1234,
      entry: name,
      files: 1,
      source: "agent" as const,
      publishedAt: Date.now() - 60_000 * (versions - i),
      ...over,
    })),
  };
}

type Route = (url: string, init?: RequestInit) => Response | Promise<Response>;

/** Answers fetches by URL; anything unrouted is a 404, so a test that forgot
    a route fails loudly rather than hanging. */
function serve(routes: Record<string, string | Route>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const route = routes[url];
    if (route === undefined) return new Response(JSON.stringify({ error: `no route ${url}` }), { status: 404 });
    return typeof route === "string" ? new Response(route) : route(url, init);
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function mount(a: Artefact, over: Partial<Parameters<typeof ArtefactSurface>[0]> = {}) {
  const onVersionChange = vi.fn();
  const view = render(<ArtefactSurface sessionId="s1" artefact={a} onVersionChange={onVersionChange} {...over} />);
  return { onVersionChange, ...view };
}

const radio = (name: string) => screen.getByRole("radio", { name });
const frame = () => document.querySelector("iframe");

beforeEach(() => {
  localStorage.clear();
  forgetTextCache();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("choosing a viewer", () => {
  it("renders markdown, and flips to its source and back", async () => {
    const a = artefact("notes.md", "text/markdown");
    serve({ [rawUrl("s1", a.id, 1, "notes.md")]: "# Hello\n\nSome *words*." });
    mount(a);
    expect(await screen.findByRole("heading", { name: "Hello" })).toBeTruthy();

    fireEvent.click(radio("Source"));
    expect(screen.queryByRole("heading", { name: "Hello" })).toBeNull();
    expect(screen.getByText("# Hello")).toBeTruthy();

    fireEvent.click(radio("Preview"));
    expect(screen.getByRole("heading", { name: "Hello" })).toBeTruthy();
  });

  it("reads the file once for both preview and source", async () => {
    const a = artefact("notes.md", "text/markdown");
    const fetch = serve({ [rawUrl("s1", a.id, 1, "notes.md")]: "# Hi" });
    mount(a);
    await screen.findByRole("heading", { name: "Hi" });
    fireEvent.click(radio("Source"));
    fireEvent.click(radio("Preview"));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("remembers the source choice for that kind of file only", async () => {
    const md = artefact("a.md", "text/markdown");
    const md2 = artefact("b.md", "text/markdown");
    const csv = artefact("c.csv", "text/csv");
    serve({
      [rawUrl("s1", md.id, 1, "a.md")]: "# A",
      [rawUrl("s1", md2.id, 1, "b.md")]: "# B",
      [rawUrl("s1", csv.id, 1, "c.csv")]: "col\nval",
    });
    const first = mount(md);
    await screen.findByRole("heading", { name: "A" });
    fireEvent.click(radio("Source"));
    first.unmount();

    const second = mount(md2);
    expect(await screen.findByText("# B")).toBeTruthy();
    expect(radio("Source").getAttribute("aria-checked")).toBe("true");
    second.unmount();

    mount(csv);
    expect(await screen.findByRole("columnheader", { name: "col" })).toBeTruthy();
    expect(screen.getByRole("cell", { name: "val" })).toBeTruthy();
  });

  it("shows a PDF inline on a desktop and as a card to open on a phone", async () => {
    const a = artefact("paper.pdf", "application/pdf");
    const raw = rawUrl("s1", a.id, 1, "paper.pdf");
    serve({});
    const desk = mount(a);
    expect(frame()?.getAttribute("src")).toBe(raw);
    desk.unmount();

    viewport("phone");
    mount(a);
    expect(frame()).toBeNull();
    expect(screen.getByRole("link", { name: "Open PDF" }).getAttribute("href")).toBe(raw);
  });

  it("pretty-prints JSON, and shows it as written when it will not parse", async () => {
    const good = artefact("d.json", "application/json");
    const bad = artefact("e.json", "application/json");
    serve({
      [rawUrl("s1", good.id, 1, "d.json")]: '{"a":1}',
      [rawUrl("s1", bad.id, 1, "e.json")]: "{nope",
    });
    const first = mount(good);
    expect(await screen.findByText('"a": 1')).toBeTruthy();
    first.unmount();

    mount(bad);
    expect(await screen.findByText("{nope")).toBeTruthy();
  });

  it("offers a download for a type it cannot show", () => {
    const a = artefact("deck.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document");
    const fetch = serve({});
    mount(a);
    const links = screen.getAllByRole("link", { name: /download/i });
    expect(links.every((l) => l.getAttribute("href") === rawUrl("s1", a.id, 1, "deck.docx", true))).toBe(true);
    // Nothing is fetched for a type with no viewer.
    expect(fetch).not.toHaveBeenCalled();
  });

  it("offers no preview/source switch where there is only one form", async () => {
    const a = artefact("main.go", "text/x-go");
    serve({ [rawUrl("s1", a.id, 1, "main.go")]: "package main\n" });
    mount(a);
    expect(await screen.findByText("package main")).toBeTruthy();
    expect(screen.queryByRole("radio", { name: "Source" })).toBeNull();
  });

  it("reports a failed read and reads again on retry", async () => {
    const a = artefact("log.txt", "text/plain");
    let calls = 0;
    serve({
      [rawUrl("s1", a.id, 1, "log.txt")]: () =>
        ++calls === 1
          ? new Response(JSON.stringify({ error: "the disk is on fire" }), { status: 500 })
          : new Response("second time lucky"),
    });
    mount(a);
    expect(await screen.findByText("the disk is on fire")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("second time lucky")).toBeTruthy();
  });
});

describe("html", () => {
  it("loads the page from a preview URL, not the raw route", async () => {
    const a = artefact("index.html", "text/html");
    const fetch = serve({
      [`/api/sessions/s1/artefacts/${a.id}/v/1/preview`]: () =>
        new Response(JSON.stringify({ url: "/p/tok1/index.html", expiresAt: Date.now() + 3_600_000 })),
    });
    mount(a);
    await waitFor(() => expect(frame()?.getAttribute("src")).toBe("/p/tok1/index.html"));
    expect(screen.getByTestId("url-pill").textContent).toBe("/index.html");
    expect(fetch).toHaveBeenCalledWith(`/api/sessions/s1/artefacts/${a.id}/v/1/preview`, expect.objectContaining({ method: "POST" }));
    // The new-tab link opens the page in its sandboxed form too.
    fireEvent.pointerDown(screen.getByRole("button", { name: "More" }), { button: 0, ctrlKey: false });
    expect((await screen.findByRole("menuitem", { name: "Open in a new tab" })).getAttribute("href")).toBe("/p/tok1/index.html");
  });

  it("says why when a preview cannot be had, and asks again on retry", async () => {
    const a = artefact("index.html", "text/html");
    let calls = 0;
    serve({
      [`/api/sessions/s1/artefacts/${a.id}/v/1/preview`]: () =>
        ++calls === 1
          ? new Response(JSON.stringify({ error: "preview unavailable" }), { status: 503 })
          : new Response(JSON.stringify({ url: "/p/t2/index.html", expiresAt: Date.now() + 3_600_000 })),
    });
    mount(a);
    expect(await screen.findByText("preview unavailable")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(frame()?.getAttribute("src")).toBe("/p/t2/index.html"));
  });
});

describe("versions", () => {
  it("shows the version asked for and reports a pick of another", async () => {
    const a = artefact("r.txt", "text/plain", 3);
    serve({
      [rawUrl("s1", a.id, 2, "r.txt")]: "version two",
      [rawUrl("s1", a.id, 3, "r.txt")]: "version three",
    });
    const { onVersionChange } = mount(a, { version: 2 });
    expect(await screen.findByText("version two")).toBeTruthy();

    fireEvent.pointerDown(screen.getByRole("button", { name: /^Version: v2$/ }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /v3.*latest/ }));
    expect(onVersionChange).toHaveBeenCalledWith(3);
  });
});

describe("sharing", () => {
  it("links to the latest or pins this version, as chosen", async () => {
    const a = artefact("r.txt", "text/plain", 2);
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const bodies: unknown[] = [];
    serve({
      [rawUrl("s1", a.id, 2, "r.txt")]: "x",
      [`/api/sessions/s1/artefacts/${a.id}/share`]: (_url, init) => {
        const body = JSON.parse(String(init?.body));
        bodies.push(body);
        const url = body.version ? `https://h/s/pinned` : `https://h/s/latest`;
        return new Response(JSON.stringify({ url, expiresAt: Date.now() + 7 * 86_400_000 }));
      },
    });
    mount(a);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));

    fireEvent.click(await screen.findByRole("button", { name: "Copy link" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://h/s/latest"));

    fireEvent.click(radio("This version (v2)"));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /Copy link|Copied/ }));
    });
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://h/s/pinned"));
    expect(bodies).toEqual([{}, { version: 2 }]);
  });
});

describe("readText", () => {
  it("stops at the cap when the server ignores the range and sends everything", async () => {
    const big = "x".repeat(TEXT_CAP + 10);
    serve({ "/f": big });
    const read = await readText("/f");
    expect(read.text.length).toBe(TEXT_CAP);
    expect(read.truncated).toBe(true);
  });

  it("knows a ranged answer was cut short from its total", async () => {
    serve({
      "/f": () =>
        new Response("abc", { status: 206, headers: { "Content-Range": `bytes 0-2/${TEXT_CAP * 3}` } }),
    });
    expect(await readText("/f")).toEqual({ text: "abc", truncated: true });
  });

  it("reads an empty file as empty", async () => {
    serve({ "/f": () => new Response(null, { status: 416 }) });
    expect(await readText("/f")).toEqual({ text: "", truncated: false });
  });
});
