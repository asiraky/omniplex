// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { quoteTerminalOutput } from "~/lib/terminalQuote";
import { render } from "~/test/harness";

import { TerminalSurface } from "./TerminalSurface";

const { FakeTerminal, socket, toast } = vi.hoisted(() => {
  // ASCII rows, one character a cell, as much of xterm's buffer as the
  // surface reads.
  const line = (text: string, wrapped = false) => ({
    isWrapped: wrapped,
    length: text.length,
    getCell: (x: number) =>
      x < text.length
        ? { getChars: () => text[x], getWidth: () => 1 }
        : undefined,
    translateToString: (trim?: boolean) =>
      trim ? text.replace(/\s+$/, "") : text,
  });

  class FakeTerminal {
    static last: FakeTerminal;
    cols = 40;
    rows = 4;
    modes = { mouseTrackingMode: "none" };
    element = document.createElement("div");
    lines = [line("")];
    selection = "";
    provider?: { provideLinks(y: number, cb: (links?: any[]) => void): void };
    private onSelection?: () => void;
    buffer = {
      active: {
        getLine: (y: number) => this.lines[y],
        length: 1,
        viewportY: 0,
      },
    };

    constructor(readonly options: any) {
      FakeTerminal.last = this;
      const screenEl = document.createElement("div");
      screenEl.className = "xterm-screen";
      // 40 × 4 cells of 10 × 20px at the page's origin.
      screenEl.getBoundingClientRect = () =>
        ({ left: 0, top: 0, width: 400, height: 80 }) as DOMRect;
      this.element.appendChild(screenEl);
    }

    show(...rows: string[]) {
      this.lines = rows.map((r) => line(r));
      this.buffer.active.length = rows.length;
    }
    select(text: string) {
      this.selection = text;
      this.onSelection?.();
    }

    loadAddon() {}
    open(host: HTMLElement) {
      host.appendChild(this.element);
    }
    registerLinkProvider(p: FakeTerminal["provider"]) {
      this.provider = p;
      return { dispose() {} };
    }
    onSelectionChange(cb: () => void) {
      this.onSelection = cb;
      return { dispose() {} };
    }
    onData() {
      return { dispose() {} };
    }
    onResize() {
      return { dispose() {} };
    }
    hasSelection() {
      return this.selection !== "";
    }
    getSelection() {
      return this.selection;
    }
    clearSelection() {
      this.select("");
    }
    focus() {}
    write() {}
    reset() {}
    dispose() {}
  }
  const socket = {
    resize() {},
    send() {},
    dispose() {},
    restart() {},
    where: vi.fn(),
  };
  return { FakeTerminal, socket, toast: { info: vi.fn() } };
});

vi.mock("@xterm/xterm", () => ({ Terminal: FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));
vi.mock("~/lib/termSocket", () => ({ openTermSocket: () => socket }));
vi.mock("~/lib/toast", () => ({ toast }));

function mount(props: Partial<Parameters<typeof TerminalSurface>[0]> = {}) {
  const { unmount } = render(
    <TerminalSurface termId="t1" target={{ thread: "s1" }} {...props} />,
  );
  return Object.assign(FakeTerminal.last, { unmount });
}

function linksOn(term: InstanceType<typeof FakeTerminal>, y: number) {
  let links: any[] | undefined;
  term.provider!.provideLinks(y, (l) => (links = l));
  return links;
}

function tap(x: number, y: number) {
  const host = FakeTerminal.last.element.parentElement!;
  const at = {
    pointerType: "touch",
    isPrimary: true,
    pointerId: 1,
    clientX: x,
    clientY: y,
  };
  fireEvent.pointerDown(host, at);
  fireEvent.pointerUp(host, at);
}

let open: ReturnType<typeof vi.fn>;
beforeEach(() => {
  toast.info.mockClear();
  socket.where.mockReset().mockResolvedValue(null);
  open = vi.fn();
  vi.stubGlobal("open", open);
});

describe("TerminalSurface: add to chat", () => {
  it("quotes the selection into the chat and clears it", () => {
    const onAddToChat = vi.fn();
    const term = mount({ onAddToChat });
    expect(screen.queryByRole("button", { name: "Add to chat" })).toBeNull();

    act(() => term.select("$ make\nok\n"));
    fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));

    expect(onAddToChat).toHaveBeenCalledWith(
      quoteTerminalOutput("$ make\nok\n"),
    );
    expect(term.selection).toBe("");
    expect(screen.queryByRole("button", { name: "Add to chat" })).toBeNull();
  });

  it("says so for a blank selection, and keeps it", () => {
    const onAddToChat = vi.fn();
    const term = mount({ onAddToChat });

    act(() => term.select("  \n \n"));
    fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));

    expect(onAddToChat).not.toHaveBeenCalled();
    expect(toast.info).toHaveBeenCalledOnce();
    expect(term.selection).toBe("  \n \n");
  });

  it("offers nothing to a caller with no chat to add to", () => {
    const term = mount();
    act(() => term.select("text"));
    expect(screen.queryByRole("button", { name: "Add to chat" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Send recent output to chat" }),
    ).toBeNull();
  });

  async function sendLast(label: string) {
    fireEvent.pointerDown(
      screen.getByRole("button", { name: "Send recent output to chat" }),
      {
        button: 0,
        ctrlKey: false,
      },
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: label }));
  }

  it("sends the last lines of the buffer", async () => {
    const onAddToChat = vi.fn();
    const term = mount({ onAddToChat });
    term.show(...Array.from({ length: 25 }, (_, i) => `line ${i}`));

    await sendLast("Last 20 lines");

    const expected = Array.from({ length: 20 }, (_, i) => `line ${i + 5}`).join(
      "\n",
    );
    expect(onAddToChat).toHaveBeenCalledWith(quoteTerminalOutput(expected));
  });

  it("says so rather than sending an empty quote", async () => {
    const onAddToChat = vi.fn();
    const term = mount({ onAddToChat });
    term.show("", "   ");

    await sendLast("Last 50 lines");

    expect(onAddToChat).not.toHaveBeenCalled();
    expect(toast.info).toHaveBeenCalledOnce();
  });
});

describe("TerminalSurface: links", () => {
  it("links paths only when there is somewhere to open them", () => {
    const term = mount();
    term.show("see https://a.io and src/app.ts:12");
    expect(linksOn(term, 1)?.map((l) => l.text)).toEqual(["https://a.io"]);
  });

  it("opens a URL in a new tab and a path through the caller", async () => {
    const onOpenPath = vi.fn();
    const term = mount({ onOpenPath });
    term.show("see https://a.io and src/app.ts:12");

    const links = linksOn(term, 1)!;
    links
      .find((l) => l.text === "https://a.io")
      .activate(new MouseEvent("mouseup"), "https://a.io");
    expect(open).toHaveBeenCalledWith(
      "https://a.io",
      "_blank",
      "noopener,noreferrer",
    );

    const path = links.find((l) => l.text.startsWith("src/"));
    path.activate(new MouseEvent("mouseup"), path.text);
    // The shell's directory is not known, so the path goes as printed.
    await waitFor(() =>
      expect(onOpenPath).toHaveBeenCalledWith("src/app.ts", 12),
    );
  });

  // The thread sits in /r/web of a repo at /r: the shell starts at /r, and
  // the panel's paths are relative to /r.
  const inWeb = { cwd: "/r/web", root: "/r" };
  async function click(text: string, at: typeof inWeb | null) {
    socket.where.mockResolvedValue(at);
    const onOpenPath = vi.fn();
    const term = mount({ onOpenPath });
    term.show(text);
    for (const link of linksOn(term, 1)!)
      link.activate(new MouseEvent("mouseup"), link.text);
    await waitFor(() => expect(onOpenPath).toHaveBeenCalled());
    term.unmount();
    return onOpenPath;
  }

  it("opens a relative path where the shell is, as a path under its start directory", async () => {
    const onOpenPath = await click(
      "../go.mod:3 and src/app.ts and dist/",
      inWeb,
    );
    await waitFor(() => expect(onOpenPath).toHaveBeenCalledTimes(3));
    expect(onOpenPath).toHaveBeenCalledWith("go.mod", 3);
    expect(onOpenPath).toHaveBeenCalledWith("web/src/app.ts", undefined);
    // git status's directory: the panel matches it without the slash.
    expect(onOpenPath).toHaveBeenCalledWith("web/dist", undefined);
  });

  it("opens an absolute path under the start directory relative to it", async () => {
    expect(await click("/r/go.mod:3", inWeb)).toHaveBeenCalledWith("go.mod", 3);
  });

  it("hands on a path outside the start directory absolute", async () => {
    expect(await click("/etc/hosts", inWeb)).toHaveBeenCalledWith(
      "/etc/hosts",
      undefined,
    );
    // A sibling whose name starts with the root's is still outside it.
    expect(await click("/r-old/a.ts", inWeb)).toHaveBeenCalledWith(
      "/r-old/a.ts",
      undefined,
    );
    // Printed after a cd out of the checkout.
    expect(
      await click("logs/out.txt", { cwd: "/tmp", root: "/r" }),
    ).toHaveBeenCalledWith("/tmp/logs/out.txt", undefined);
  });

  it("hands on a path as printed when the shell cannot say where it is", async () => {
    expect(await click("src/app.ts:12", null)).toHaveBeenCalledWith(
      "src/app.ts",
      12,
    );
    expect(await click("/r/go.mod", null)).toHaveBeenCalledWith(
      "/r/go.mod",
      undefined,
    );
  });

  it("asks the shell nothing for a home path", async () => {
    expect(await click("~/notes.md", inWeb)).toHaveBeenCalledWith(
      "~/notes.md",
      undefined,
    );
    expect(socket.where).not.toHaveBeenCalled();
  });

  it("opens nothing when the terminal is gone by the time the shell answers", async () => {
    let answer!: (at: { cwd: string; root: string }) => void;
    socket.where.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const onOpenPath = vi.fn();
    const term = mount({ onOpenPath });
    term.show("src/app.ts:12");

    linksOn(term, 1)![0].activate(new MouseEvent("mouseup"), "src/app.ts:12");
    term.unmount();
    await act(async () => answer({ cwd: "/r/web", root: "/r" }));

    expect(onOpenPath).not.toHaveBeenCalled();
  });

  it("does not follow a link when the click made a selection", () => {
    const term = mount();
    term.show("go https://a.io now");
    // A double-click that selected the URL ends in a mouseup over it.
    act(() => term.select("https://a.io"));
    linksOn(term, 1)![0].activate(new MouseEvent("mouseup"), "https://a.io");
    expect(open).not.toHaveBeenCalled();
  });

  it("answers undefined for a row with no links", () => {
    const term = mount();
    term.show("nothing here");
    expect(linksOn(term, 1)).toBeUndefined();
  });

  it("opens a tapped link once, though xterm's own activation follows the tap", () => {
    const term = mount();
    term.show("go https://a.io now");
    // Cell 6 of row 1 is inside the URL: cells are 10px wide, 20px tall.
    tap(55, 10);
    expect(open).toHaveBeenCalledOnce();

    linksOn(term, 1)![0].activate(new MouseEvent("mouseup"), "https://a.io");
    expect(open).toHaveBeenCalledOnce();
  });

  it("resolves a tapped path against the shell's directory too", async () => {
    socket.where.mockResolvedValue({ cwd: "/r/web", root: "/r" });
    const onOpenPath = vi.fn();
    const term = mount({ onOpenPath });
    term.show("at src/app.ts:12");
    tap(55, 10);
    await waitFor(() =>
      expect(onOpenPath).toHaveBeenCalledWith("web/src/app.ts", 12),
    );
  });

  it("ignores a tap beside a link, a drag, and a tap while an app reads the mouse", () => {
    const term = mount();
    term.show("go https://a.io now");
    const host = term.element.parentElement!;

    tap(10, 10); // "g"
    fireEvent.pointerDown(host, {
      pointerType: "touch",
      isPrimary: true,
      pointerId: 1,
      clientX: 55,
      clientY: 10,
    });
    // A scroll that starts and ends on the link.
    fireEvent.pointerUp(host, {
      pointerType: "touch",
      isPrimary: true,
      pointerId: 1,
      clientX: 85,
      clientY: 10,
    });
    term.modes.mouseTrackingMode = "vt200";
    tap(55, 10);

    expect(open).not.toHaveBeenCalled();
  });
});
