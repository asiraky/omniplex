// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { renderMermaid } from "./mermaid";

const { render } = vi.hoisted(() => ({ render: vi.fn() }));
vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    mermaidAPI: { getConfig: () => ({}) },
    render,
  },
}));
const svg =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 200"></svg>';
beforeEach(() => {
  render.mockReset().mockResolvedValue({ svg });
});
afterEach(() => {
  document.body.replaceChildren();
});

it("serializes rendering so overlapping diagrams cannot interfere", async () => {
  let finish!: (value: { svg: string }) => void;
  render.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const first = renderMermaid("flowchart LR\nA --> B", "dark", () => false);
  await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(1));
  const second = renderMermaid("flowchart LR\nC --> D", "light", () => false);
  await Promise.resolve();
  expect(render).toHaveBeenCalledTimes(1);
  finish({ svg });
  const images = await Promise.all([first, second]);
  expect(render).toHaveBeenCalledTimes(2);
  expect(
    images.every((value) => value?.startsWith("data:image/svg+xml,")),
  ).toBe(true);
  expect(document.body.childElementCount).toBe(0);
});

it("cleans up failed renders and continues rendering later diagrams", async () => {
  render.mockRejectedValueOnce(new Error("parse error"));
  await expect(renderMermaid("invalid", "light", () => false)).rejects.toThrow(
    "parse error",
  );
  expect(document.body.childElementCount).toBe(0);
  expect(
    await renderMermaid("flowchart LR\nA --> B", "light", () => false),
  ).toBeTruthy();
});

it("skips obsolete jobs waiting in the queue", async () => {
  expect(
    await renderMermaid("flowchart LR\nA --> B", "light", () => true),
  ).toBeNull();
  expect(render).not.toHaveBeenCalled();
});

it.each([
  'flowchart LR\nA@{ img: "https://example.com/tracker.png" }',
  'flowchart LR\nA@{ "img": "https://example.com/tracker.png" }',
  'flowchart LR\nA@{ "\\u0069mg": "https://example.com/tracker.png" }',
  "flowchart LR\nA --> B\nstyle A fill:u\\72l(https://example.com/tracker.svg)",
  "flowchart LR\nA --> B\nstyle A fill:url(https://example.com/tracker.svg)",
  '%%{init: {"themeCSS":"@import https://example.com/tracker.css"}}%%\nflowchart LR\nA --> B',
  "a".repeat(50_001),
])(
  "rejects resource-loading or oversized input before rendering",
  async (code) => {
    await expect(renderMermaid(code, "light", () => false)).rejects.toThrow();
    expect(render).not.toHaveBeenCalled();
    expect(document.body.childElementCount).toBe(0);
  },
);
