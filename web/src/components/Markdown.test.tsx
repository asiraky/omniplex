// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { Markdown } from "./Markdown";
import { renderMermaid } from "~/lib/mermaid";
import { useTheme } from "~/lib/theme";
import { render, wrap } from "~/test/harness";

vi.mock("~/lib/mermaid", () => ({ renderMermaid: vi.fn() }));
const renderer = vi.mocked(renderMermaid);
const diagram = (code: string, closed = true) =>
  `\`\`\`mermaid\n${code}${closed ? "\n```" : ""}`;
const image = "data:image/svg+xml,test";
async function settle() {
  await act(async () => {
    await vi.dynamicImportSettled();
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  renderer.mockReset().mockResolvedValue(image);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(document.documentElement, "requestFullscreen");
  Reflect.deleteProperty(document, "exitFullscreen");
  Reflect.deleteProperty(document, "fullscreenElement");
});

it("opens a full-screen viewer with zoom controls and restores focus on close", async () => {
  render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  const expand = screen.getByLabelText("Expand diagram to full screen");
  fireEvent.click(expand);
  expect(screen.getByRole("dialog", { name: "Mermaid diagram" })).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Zoom in"));
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("125%");
  fireEvent.click(screen.getByLabelText("Zoom out"));
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("100%");
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "+" });
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("125%");
  fireEvent.click(screen.getByLabelText("Fit diagram"));
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("100%");
  fireEvent.click(screen.getByLabelText("Close full screen"));
  await settle();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(expand);
});

it("requests browser fullscreen inside the expand click and closes when the browser exits", async () => {
  const root = document.documentElement;
  Object.defineProperty(document, "fullscreenElement", {
    value: null,
    writable: true,
    configurable: true,
  });
  const request = vi.fn(async () => {
    Object.defineProperty(document, "fullscreenElement", {
      value: root,
      writable: true,
      configurable: true,
    });
    document.dispatchEvent(new Event("fullscreenchange"));
  });
  root.requestFullscreen = request;
  render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  expect(request).toHaveBeenCalledTimes(1);
  await act(async () => {
    Object.defineProperty(document, "fullscreenElement", {
      value: null,
      writable: true,
      configurable: true,
    });
    document.dispatchEvent(new Event("fullscreenchange"));
  });
  await settle();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps the viewer usable when the browser rejects fullscreen", async () => {
  document.documentElement.requestFullscreen = vi
    .fn()
    .mockRejectedValue(new Error("unsupported"));
  render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  await settle();
  expect(screen.getByRole("dialog")).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Zoom in"));
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("125%");
});

it("ignores a stale fullscreen rejection after reopening and exits the newer fullscreen", async () => {
  let rejectFirst!: (reason: Error) => void;
  const root = document.documentElement;
  Object.defineProperty(document, "fullscreenElement", {
    value: null,
    writable: true,
    configurable: true,
  });
  root.requestFullscreen = vi
    .fn()
    .mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          rejectFirst = reject;
        }),
    )
    .mockImplementationOnce(async () => {
      Object.defineProperty(document, "fullscreenElement", {
        value: root,
        writable: true,
        configurable: true,
      });
      document.dispatchEvent(new Event("fullscreenchange"));
    });
  const exit = vi.fn(async () => {
    Object.defineProperty(document, "fullscreenElement", {
      value: null,
      writable: true,
      configurable: true,
    });
    document.dispatchEvent(new Event("fullscreenchange"));
  });
  document.exitFullscreen = exit;
  render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  fireEvent.click(screen.getByLabelText("Close full screen"));
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  await act(async () => {
    rejectFirst(new Error("old request rejected"));
  });
  fireEvent.click(screen.getByLabelText("Close full screen"));
  expect(exit).toHaveBeenCalledTimes(1);
  expect(document.fullscreenElement).toBeNull();
});

it("waits for an in-flight fullscreen exit before reopening", async () => {
  const root = document.documentElement;
  Object.defineProperty(document, "fullscreenElement", {
    value: null,
    writable: true,
    configurable: true,
  });
  const request = vi.fn(async () => {
    Object.defineProperty(document, "fullscreenElement", {
      value: root,
      writable: true,
      configurable: true,
    });
    document.dispatchEvent(new Event("fullscreenchange"));
  });
  root.requestFullscreen = request;
  let finishExit!: () => void;
  document.exitFullscreen = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        finishExit = () => {
          Object.defineProperty(document, "fullscreenElement", {
            value: null,
            writable: true,
            configurable: true,
          });
          document.dispatchEvent(new Event("fullscreenchange"));
          resolve();
        };
      }),
  );
  const view = render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  await settle();
  fireEvent.click(screen.getByLabelText("Close full screen"));
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  expect(request).toHaveBeenCalledTimes(1);
  await act(async () => {
    finishExit();
  });
  expect(request).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("dialog")).toBeTruthy();
  view.unmount();
  await act(async () => {
    finishExit();
  });
});

it("fits the image after the dialog portal mounts and scales the fitted size", async () => {
  let measure!: () => void;
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private callback: ResizeObserverCallback) {}
      observe(el: Element) {
        if (el.getAttribute("aria-label") === "Expanded Mermaid diagram") {
          measure = () =>
            this.callback(
              [
                {
                  contentRect: { width: 390, height: 800 },
                } as ResizeObserverEntry,
              ],
              this as unknown as ResizeObserver,
            );
        }
      }
      unobserve() {}
      disconnect() {}
    },
  );
  render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  const img = screen.getByRole("img") as HTMLImageElement;
  Object.defineProperties(img, {
    naturalWidth: { value: 800 },
    naturalHeight: { value: 200 },
  });
  fireEvent.load(img);
  act(() => measure());
  expect(img.style.width).toBe("358px");
  fireEvent.click(screen.getByLabelText("Zoom in"));
  expect(img.style.width).toBe("447.5px");
  fireEvent.click(screen.getByLabelText("Fit diagram"));
  expect(img.style.width).toBe("358px");
});

it("bounds zoom, resets it on reopen, and hides expand when rendering fails", async () => {
  const view = render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  for (let i = 0; i < 30; i++)
    fireEvent.click(screen.getByLabelText("Zoom in"));
  expect((screen.getByLabelText("Zoom in") as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("800%");
  for (let i = 0; i < 40; i++)
    fireEvent.click(screen.getByLabelText("Zoom out"));
  expect(
    (screen.getByLabelText("Zoom out") as HTMLButtonElement).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByLabelText("Close full screen"));
  await settle();
  fireEvent.click(screen.getByLabelText("Expand diagram to full screen"));
  expect(screen.getByLabelText("Diagram zoom").textContent).toBe("100%");
  fireEvent.click(screen.getByLabelText("Close full screen"));
  renderer.mockRejectedValueOnce(new Error("invalid"));
  view.rerender(wrap(<Markdown text={diagram("invalid")} />));
  await settle();
  expect(screen.queryByLabelText("Expand diagram to full screen")).toBeNull();
});

it("renders Mermaid fences as diagrams, retaining copyable source", async () => {
  const code = "flowchart LR\nA --> B";
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(<Markdown text={diagram(code)} />);
  await settle();
  expect(screen.getByRole("img").getAttribute("src")).toBe(image);
  expect(
    screen.getByText("flowchart LR A --> B").closest("details"),
  ).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Copy code"));
  expect(writeText).toHaveBeenCalledWith(code);
});

it("leaves ordinary fenced and inline code alone without loading Mermaid", async () => {
  render(<Markdown text={"```js\nconst x = 1;\n```\n\n`mermaid`"} />);
  await settle();
  expect(screen.getByText("const x = 1;").tagName).toBe("CODE");
  expect(screen.getByText("mermaid").tagName).toBe("CODE");
  expect(renderer).not.toHaveBeenCalled();
});

it("debounces streaming text and renders an unfinished fence", async () => {
  const view = render(<Markdown text={diagram("flowchart LR\nA -", false)} />);
  view.rerender(
    wrap(<Markdown text={diagram("flowchart LR\nA --> B", false)} />),
  );
  await settle();
  expect(renderer).toHaveBeenCalledTimes(1);
  expect(renderer.mock.calls[0][0]).toBe("flowchart LR\nA --> B");
  expect(screen.getByRole("img")).toBeTruthy();
});

it("falls back to source after a failed render, then recovers as text arrives", async () => {
  renderer.mockRejectedValueOnce(new Error("parse error"));
  const view = render(<Markdown text={diagram("flowchart LR\nA -->[")} />);
  await settle();
  expect(screen.getByRole("status").textContent).toContain("Couldn't render");
  expect(screen.getByText("flowchart LR A -->[")).toBeTruthy();
  view.rerender(wrap(<Markdown text={diagram("flowchart LR\nA --> B")} />));
  await settle();
  expect(screen.getByRole("img")).toBeTruthy();
});

it("ignores an old asynchronous render that finishes after a newer one", async () => {
  let finish!: (value: string) => void;
  renderer.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  view.rerender(wrap(<Markdown text={diagram("flowchart LR\nA --> C")} />));
  await settle();
  await act(async () => {
    finish("data:image/svg+xml,old");
  });
  expect(screen.getByRole("img").getAttribute("src")).toBe(image);
  expect(renderer.mock.calls[0][2]()).toBe(true);
});

it("cancels pending work when a diagram unmounts", async () => {
  const view = render(<Markdown text={diagram("flowchart LR\nA --> B")} />);
  await settle();
  const cancelled = renderer.mock.calls[0][2];
  view.unmount();
  expect(cancelled()).toBe(true);
});

it("renders Mermaid in Markdown documents too", async () => {
  render(
    <Markdown document text={diagram("sequenceDiagram\nAlice->>Bob: Hi")} />,
  );
  await settle();
  expect(screen.getByRole("img")).toBeTruthy();
});

it("rerenders diagrams when the app theme changes", async () => {
  function Example() {
    const { setTheme } = useTheme();
    return (
      <>
        <button onClick={() => setTheme("dark")}>Dark theme</button>
        <Markdown text={diagram("flowchart LR\nA --> B")} />
      </>
    );
  }
  render(<Example />);
  await settle();
  fireEvent.click(screen.getByText("Dark theme"));
  expect(screen.queryByRole("img")).toBeNull();
  await settle();
  expect(renderer.mock.calls.at(-1)?.[1]).toBe("dark");
  expect(screen.getByRole("img")).toBeTruthy();
  localStorage.clear();
});
