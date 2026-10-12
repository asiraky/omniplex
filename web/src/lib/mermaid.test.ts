// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { renderMermaid } from "./mermaid";

const { render, parse } = vi.hoisted(() => ({
  render: vi.fn(),
  parse: vi.fn(),
}));
vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    mermaidAPI: { getConfig: () => ({}), getDiagramFromText: parse },
    render,
  },
}));
const svg =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 200"></svg>';
beforeEach(() => {
  render.mockReset().mockResolvedValue({ svg });
  parse.mockReset().mockResolvedValue({ db: {} });
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
  { nodes: new Map([["a", { img: "https://example.com/tracker.png" }]]) },
  { getData: () => ({ nodes: [{ img: "https://example.com/tracker.png" }] }) },
  {
    getActors: () =>
      new Map([
        ["a", { properties: { icon: "https://example.com/tracker.png" } }],
      ]),
  },
  {
    getClasses: () => ({
      red: { styles: ["fill:u\\72l(https://example.com/tracker.svg)"] },
    }),
  },
  { nodes: [{ styles: ["fill:url(https://example.com/tracker.svg)"] }] },
  { themeCSS: "@import https://example.com/tracker.css" },
])("rejects parsed resource attributes before rendering", async (db) => {
  parse.mockResolvedValue({ db });
  await expect(
    renderMermaid("diagram", "light", () => false),
  ).rejects.toThrow();
  expect(render).not.toHaveBeenCalled();
  expect(document.body.childElementCount).toBe(0);
});

it("allows labels and messages that describe image fields and URL calls", async () => {
  parse.mockResolvedValue({
    db: { nodes: [{ label: "Call url()", text: "img: upload" }] },
  });
  expect(
    await renderMermaid(
      'flowchart LR\nA["Call url()"] --> B["img: upload"]',
      "light",
      () => false,
    ),
  ).toBeTruthy();
  expect(render).toHaveBeenCalledTimes(1);
});

it("rejects oversized input before parsing or rendering", async () => {
  await expect(
    renderMermaid("a".repeat(50_001), "light", () => false),
  ).rejects.toThrow();
  expect(parse).not.toHaveBeenCalled();
  expect(render).not.toHaveBeenCalled();
});
