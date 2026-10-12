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
