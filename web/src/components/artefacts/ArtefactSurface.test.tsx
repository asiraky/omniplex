// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ArtefactSurface } from "~/components/artefacts/ArtefactSurface";
import { forgetTextCache, readText, TEXT_CAP } from "~/components/artefacts/viewers";
import { rawUrl, type Artefact } from "~/lib/artefacts";
import { makeArtefact } from "~/test/artefact";
import { render, viewport, wrap } from "~/test/harness";

function artefact(name: string, mediaType: string, over: Partial<Artefact> = {}): Artefact {
  return makeArtefact({ id: `a-${name}`, name, mediaType, size: 1234, shownAt: Date.now() - 60_000, ...over });
}

const raw = (a: Artefact, download = false) => rawUrl("s1", a.id, a.entry, { rev: a.modifiedAt, download });

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

function mount(a: Artefact) {
  return render(<ArtefactSurface threadId="s1" artefact={a} />);
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
    serve({ [raw(a)]: "# Hello\n\nSome *words*." });
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
    const fetch = serve({ [raw(a)]: "# Hi" });
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
      [raw(md)]: "# A",
      [raw(md2)]: "# B",
      [raw(csv)]: "col\nval",
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
    const pdf = raw(a);
    serve({});
    const desk = mount(a);
    expect(frame()?.getAttribute("src")).toBe(pdf);
    desk.unmount();

    viewport("phone");
    mount(a);
    expect(frame()).toBeNull();
    expect(screen.getByRole("link", { name: "Open PDF" }).getAttribute("href")).toBe(pdf);
  });

  it("pretty-prints JSON, and shows it as written when it will not parse", async () => {
    const good = artefact("d.json", "application/json");
    const bad = artefact("e.json", "application/json");
    serve({
      [raw(good)]: '{"a":1}',
      [raw(bad)]: "{nope",
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
    expect(links.every((l) => l.getAttribute("href") === raw(a, true))).toBe(true);
    // Nothing is fetched for a type with no viewer.
    expect(fetch).not.toHaveBeenCalled();
  });

  it("offers no preview/source switch where there is only one form", async () => {
    const a = artefact("main.go", "text/x-go");
    serve({ [raw(a)]: "package main\n" });
    mount(a);
    expect(await screen.findByText("package main")).toBeTruthy();
    expect(screen.queryByRole("radio", { name: "Source" })).toBeNull();
  });

  it("reports a failed read and reads again on retry", async () => {
    const a = artefact("log.txt", "text/plain");
    let calls = 0;
    serve({
      [raw(a)]: () =>
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
      [`/api/threads/s1/artefacts/${a.id}/preview`]: () =>
        new Response(JSON.stringify({ url: "/p/tok1/index.html", expiresAt: Date.now() + 3_600_000 })),
    });
    mount(a);
    await waitFor(() => expect(frame()?.getAttribute("src")).toBe("/p/tok1/index.html"));
    expect(screen.getByTestId("url-pill").textContent).toBe("/index.html");
    expect(fetch).toHaveBeenCalledWith(`/api/threads/s1/artefacts/${a.id}/preview`, expect.objectContaining({ method: "POST" }));
    // The new-tab link opens the page in its sandboxed form too.
    fireEvent.pointerDown(screen.getByRole("button", { name: "More" }), { button: 0, ctrlKey: false });
    expect((await screen.findByRole("menuitem", { name: "Open in a new tab" })).getAttribute("href")).toBe("/p/tok1/index.html");
  });

  it("says why when a preview cannot be had, and asks again on retry", async () => {
    const a = artefact("index.html", "text/html");
    let calls = 0;
    serve({
      [`/api/threads/s1/artefacts/${a.id}/preview`]: () =>
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

describe("revisions", () => {
  it("reads the file again when the agent shows a new revision", async () => {
    const a = artefact("r.txt", "text/plain");
    const b = { ...a, modifiedAt: a.modifiedAt + 5000 };
    serve({ [raw(a)]: "first draft", [raw(b)]: "second draft" });
    const view = mount(a);
    expect(await screen.findByText("first draft")).toBeTruthy();
    view.rerender(wrap(<ArtefactSurface threadId="s1" artefact={b} />));
    expect(await screen.findByText("second draft")).toBeTruthy();
  });
});

describe("sharing", () => {
  function shareServer(a: Artefact) {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const calls: string[] = [];
    let link: { url: string; sharedAt: number; expiresAt: number } | null = null;
    let n = 0;
    serve({
      [raw(a)]: "x",
      [`/api/threads/s1/artefacts/${a.id}/share`]: (_url, init) => {
        const method = init?.method ?? "GET";
        calls.push(method);
        if (method === "POST") link = { url: link?.url ?? `https://h/s/${++n}`, sharedAt: Date.now(), expiresAt: Date.now() + 7 * 86_400_000 };
        if (method === "DELETE") link = null;
        return new Response(JSON.stringify({ share: link }));
      },
    });
    return { writeText, calls, setLink: (l: typeof link) => (link = l) };
  }

  it("shares nothing until asked, then copies, updates and stops the link", async () => {
    const a = artefact("r.txt", "text/plain");
    const { writeText, calls } = shareServer(a);
    mount(a);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));

    fireEvent.click(await screen.findByRole("button", { name: "Create link" }));
    expect(((await screen.findByRole("textbox", { name: "Share link" })) as HTMLInputElement).value).toBe("https://h/s/1");
    fireEvent.click(screen.getByRole("button", { name: "Copy link" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://h/s/1"));

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Update link" }));
    });
    expect((screen.getByRole("textbox", { name: "Share link" }) as HTMLInputElement).value).toBe("https://h/s/1");

    fireEvent.click(screen.getByRole("button", { name: "Stop sharing" }));
    expect(await screen.findByRole("button", { name: "Create link" })).toBeTruthy();
    expect(calls).toEqual(["GET", "POST", "POST", "DELETE"]);
  });

  it("says when the file has changed since the link was made", async () => {
    const a = artefact("r.txt", "text/plain", { modifiedAt: Date.now() });
    const { setLink } = shareServer(a);
    setLink({ url: "https://h/s/old", sharedAt: a.modifiedAt - 60_000, expiresAt: Date.now() + 86_400_000 });
    const view = mount(a);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByText(/changed since/)).toBeTruthy();
    view.unmount();

    setLink({ url: "https://h/s/new", sharedAt: a.modifiedAt + 1, expiresAt: Date.now() + 86_400_000 });
    mount(a);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    await screen.findByRole("textbox", { name: "Share link" });
    expect(screen.queryByText(/changed since/)).toBeNull();
  });

  it("treats an expired link as no link", async () => {
    const a = artefact("r.txt", "text/plain");
    const { setLink } = shareServer(a);
    setLink({ url: "https://h/s/old", sharedAt: 1, expiresAt: Date.now() - 1 });
    mount(a);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByRole("button", { name: "Create link" })).toBeTruthy();
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
