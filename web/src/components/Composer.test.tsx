// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import { Composer, type ComposerHandle } from "./Composer";
import type { Attachment } from "~/lib/attachments";
import type { HarnessMeta } from "~/protocol";
import type { ComposerCatalogue } from "./composer/useComposerItems";

const png = (name = "shot.png") => new File([new Uint8Array([1, 2, 3])], name, { type: "image/png" });
const pdf = (name = "b.pdf") => new File([new Uint8Array([1, 2, 3])], name, { type: "application/pdf" });

// jsdom has no DataTransfer worth using; the composer only ever asks a drop
// for its files and its types, so that is all a test has to hand it.
const transfer = (files: File[]) => ({ files, types: ["Files"] });

const staged = (over: Partial<Attachment> = {}): Attachment => ({
  key: "k1",
  name: "shot.png",
  mediaType: "image/png",
  previewUrl: "blob:preview",
  status: "ready",
  id: "img-1",
  ...over,
});

const stagedFile = (over: Partial<Attachment> = {}): Attachment => ({
  key: "k2",
  kind: "file",
  name: "b.pdf",
  previewUrl: "",
  mediaType: "application/pdf",
  size: 3,
  status: "ready",
  artefactId: "art-1",
  ...over,
});

function mount(over: Partial<React.ComponentProps<typeof Composer>> = {}) {
  const onSend = vi.fn();
  const onAttachFiles = vi.fn();
  const onRemoveAttachment = vi.fn();
  const view = render(
    <Composer
      draft=""
      onDraftChange={vi.fn()}
      disabled={false}
      busy={false}
      onSend={onSend}
      onCancel={vi.fn()}
      onAttachFiles={onAttachFiles}
      onRemoveAttachment={onRemoveAttachment}
      {...over}
    />,
  );
  const rerender = (next: Partial<React.ComponentProps<typeof Composer>> = {}) =>
    view.rerender(
      wrap(
        <Composer
          draft=""
          onDraftChange={vi.fn()}
          disabled={false}
          busy={false}
          onSend={onSend}
          onCancel={vi.fn()}
          onAttachFiles={onAttachFiles}
          onRemoveAttachment={onRemoveAttachment}
          {...over}
          {...next}
        />,
      ),
    );
  return { onSend, onAttachFiles, onRemoveAttachment, rerender };
}

// A clipboard carrying only text, as a paste from another app does.
const text = (value: string) => ({
  files: [],
  types: ["text/plain"],
  getData: (type: string) => (type === "text/plain" ? value : ""),
});
const fileInput = () => document.querySelector<HTMLInputElement>("input[type=file]")!;
// Drop and paste are handled on the card around the textarea; React events
// bubble, so firing on the box a hand would actually be over is enough.
const box = () => screen.getByLabelText("Message");
const sendButton = () => screen.getByRole("button", { name: "Send" });

describe("attaching images", () => {
  it("takes a picked file and clears the input so the same file can be picked twice", () => {
    const { onAttachFiles } = mount();
    const file = png();
    fireEvent.change(fileInput(), { target: { files: [file] } });
    expect(onAttachFiles).toHaveBeenCalledWith([file]);
    expect(fileInput().value).toBe("");
  });

  it("takes a dropped image", () => {
    const { onAttachFiles } = mount();
    const file = png();
    fireEvent.drop(box(), { dataTransfer: transfer([file]) });
    expect(onAttachFiles).toHaveBeenCalledWith([file]);
  });

  it("takes a pasted screenshot and leaves pasted text to the textarea", () => {
    const { onAttachFiles } = mount();
    fireEvent.paste(box(), { clipboardData: text("a sentence") });
    expect(onAttachFiles).not.toHaveBeenCalled();

    const file = png("clipboard.png");
    fireEvent.paste(box(), { clipboardData: { files: [file], types: ["Files"] } });
    expect(onAttachFiles).toHaveBeenCalledWith([file]);
  });

  it("takes any kind of file, however it arrives", () => {
    const { onAttachFiles } = mount();
    const picked = pdf("picked.pdf");
    const dropped = new File(["a,b"], "data.csv", { type: "text/csv" });
    const pasted = new File(["x"], "notes", { type: "" });
    fireEvent.change(fileInput(), { target: { files: [picked] } });
    fireEvent.drop(box(), { dataTransfer: transfer([dropped]) });
    fireEvent.paste(box(), { clipboardData: { files: [pasted], types: ["Files"] } });
    expect(onAttachFiles.mock.calls).toEqual([[[picked]], [[dropped]], [[pasted]]]);
  });

  it("turns a long paste into a file, and leaves it as text with shift", () => {
    const { onAttachFiles } = mount();
    const long = "# Plan\n\n" + "- a step\n".repeat(30);
    const plain = fireEvent.paste(box(), { clipboardData: text(long) });
    expect(plain).toBe(false); // handled: the textarea never sees it
    const [[[file]]] = onAttachFiles.mock.calls;
    expect(file.name).toBe("plan.md");

    fireEvent.keyDown(box(), { key: "V", ctrlKey: true, shiftKey: true });
    expect(fireEvent.paste(box(), { clipboardData: text(long) })).toBe(true);
    expect(onAttachFiles).toHaveBeenCalledTimes(1);
  });

  it("ignores a drop that carries no files", () => {
    const { onAttachFiles } = mount();
    fireEvent.drop(box(), { dataTransfer: { files: [], types: ["text/uri-list"] } });
    expect(onAttachFiles).not.toHaveBeenCalled();
  });

  it("attaches nothing while the composer is disabled", () => {
    const { onAttachFiles } = mount({ disabled: true, disabledPlaceholder: "Reconnecting" });
    fireEvent.change(fileInput(), { target: { files: [png()] } });
    expect(onAttachFiles).not.toHaveBeenCalled();
  });
});

describe("sending with images", () => {
  it("sends a message that is nothing but pictures", () => {
    const { onSend } = mount({ attachments: [staged()] });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("", { imageIds: ["img-1"], files: [] }, undefined);
  });

  it("refuses to send while an image is still going up", () => {
    const { onSend } = mount({
      draft: "look at this",
      attachments: [staged({ status: "uploading", id: undefined })],
    });
    expect(sendButton()).toHaveProperty("disabled", true);
    fireEvent.click(sendButton());
    expect(onSend).not.toHaveBeenCalled();
  });

  it("keeps send out of reach with neither text nor a ready image", () => {
    mount({ attachments: [staged({ status: "error", id: undefined, error: "too big" })] });
    expect(sendButton()).toHaveProperty("disabled", true);
  });

  it("stays writable while the workspace is being prepared, but holds the message back", () => {
    const onDraftChange = vi.fn();
    const { onSend } = mount({
      draft: "start on the parser",
      sendDisabled: true,
      disabledPlaceholder: "Preparing workspace…",
      onDraftChange,
    });
    expect(box()).toHaveProperty("disabled", false);
    fireEvent.change(box(), { target: { value: "start on the parser now" } });
    expect(onDraftChange).toHaveBeenCalledWith("start on the parser now");
    expect(sendButton()).toHaveProperty("disabled", true);
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).not.toHaveBeenCalled();
  });

  it("sends ready files by artefact alongside images", () => {
    const { onSend } = mount({ draft: "see attached", attachments: [staged(), stagedFile()] });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("see attached", {
      imageIds: ["img-1"],
      files: [{ artefactId: "art-1" }],
    }, undefined);
  });

  it("sends a message that is nothing but a file", () => {
    const { onSend } = mount({ attachments: [stagedFile()] });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("", { imageIds: [], files: [{ artefactId: "art-1" }] }, undefined);
  });

  it("refuses to send while a file is still going up, and shows how far it has got", () => {
    const { onSend } = mount({
      draft: "read this",
      attachments: [stagedFile({ status: "uploading", artefactId: undefined, progress: 0.4 })],
    });
    expect(screen.getByRole("progressbar", { name: "Uploading b.pdf" }).getAttribute("aria-valuenow")).toBe("40");
    fireEvent.click(sendButton());
    expect(onSend).not.toHaveBeenCalled();
  });

  it("does not count a file that failed to upload as something to send", () => {
    mount({ attachments: [stagedFile({ status: "error", artefactId: undefined, error: "too large" })] });
    expect(sendButton()).toHaveProperty("disabled", true);
  });

  it("removes a staged file", () => {
    const { onRemoveAttachment } = mount({ attachments: [stagedFile()] });
    fireEvent.click(screen.getByRole("button", { name: "Remove b.pdf" }));
    expect(onRemoveAttachment).toHaveBeenCalledWith("k2");
  });

  it("holds back a message whose images and PDFs together are more than the server takes", () => {
    const big = (key: string) =>
      stagedFile({ key, name: `${key}.pdf`, artefactId: undefined, id: key, size: 9_500_000 });
    const { onSend, rerender } = mount({ draft: "summarise", attachments: [big("a"), big("b")] });
    expect(sendButton()).toHaveProperty("disabled", false);

    rerender({ draft: "summarise", attachments: [big("a"), big("b"), big("c")] });
    expect(sendButton()).toHaveProperty("disabled", true);
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).not.toHaveBeenCalled();
    expect(screen.getByText(/over 20 MB in total/)).toBeTruthy();

    // A file sent as an artefact is a path to the agent, not bytes in the
    // message, so it does not count.
    rerender({ draft: "summarise", attachments: [big("a"), big("b"), stagedFile({ size: 9_500_000 })] });
    expect(sendButton()).toHaveProperty("disabled", false);
  });

  it("removes a staged image", () => {
    const { onRemoveAttachment } = mount({ attachments: [staged()] });
    fireEvent.click(screen.getByRole("button", { name: "Remove shot.png" }));
    expect(onRemoveAttachment).toHaveBeenCalledWith("k1");
  });
});

describe("the send button's options", () => {
  const openOptions = () =>
    fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), { button: 0, ctrlKey: false });

  it("sends now from the menu", async () => {
    const { onSend } = mount({ draft: "ship it", onSchedule: vi.fn() });
    openOptions();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Send now" }));
    expect(onSend).toHaveBeenCalledWith("ship it", { imageIds: [], files: [] }, undefined);
  });

  it("schedules from the menu without sending", async () => {
    const onSchedule = vi.fn();
    const { onSend } = mount({ draft: "later", onSchedule });
    openOptions();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Schedule send…" }));
    expect(onSchedule).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });

  it("will not schedule a message carrying files, which a schedule would drop", async () => {
    const onSchedule = vi.fn();
    mount({ draft: "read this later", onSchedule, attachments: [stagedFile()] });
    openOptions();
    const item = await screen.findByRole("menuitem", { name: /Schedule send/ });
    expect(item.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(item);
    expect(onSchedule).not.toHaveBeenCalled();
  });

  it("offers no options when there is nowhere to schedule", () => {
    mount({ draft: "hi" });
    expect(screen.queryByRole("button", { name: "More send options" })).toBeNull();
  });

  it("holds the options back with nothing to send", () => {
    mount({ draft: "", onSchedule: vi.fn() });
    expect(screen.getByRole("button", { name: "More send options" })).toHaveProperty("disabled", true);
  });
});

describe("steering or queueing a message sent mid-turn", () => {
  const sent = { imageIds: [], files: [] };
  const steerButton = () => screen.getByRole("button", { name: "Steer the running turn" });
  const hold = (el: HTMLElement) => {
    fireEvent.pointerDown(el, { button: 0 });
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(el);
    // The click a real release sends after the hold.
    fireEvent.click(el);
  };

  afterEach(() => {
    vi.useRealTimers();
  });

  it("steers by default, leaving the way to the server", () => {
    const { onSend } = mount({ draft: "use the other table", busy: true, steers: true });
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).toHaveBeenCalledWith("use the other table", sent, undefined);
  });

  it.each([{ ctrlKey: true }, { metaKey: true }])("queues on mod+Enter (%o)", (mod) => {
    const { onSend } = mount({ draft: "then write the docs", busy: true, steers: true });
    fireEvent.keyDown(box(), { key: "Enter", ...mod });
    expect(onSend).toHaveBeenCalledWith("then write the docs", sent, "queue");
  });

  it("treats mod+Enter as Enter for a harness that can only queue", () => {
    const { onSend } = mount({ draft: "next", busy: true, steers: false });
    fireEvent.keyDown(box(), { key: "Enter", ctrlKey: true });
    expect(onSend).toHaveBeenCalledWith("next", sent, undefined);
  });

  // A turn just started may not have reached this client yet; the server
  // knows, and starts a turn with the send if none is running.
  it("asks to queue on mod+Enter even when no turn looks to be running", () => {
    const { onSend } = mount({ draft: "start", steers: true });
    fireEvent.keyDown(box(), { key: "Enter", ctrlKey: true });
    expect(onSend).toHaveBeenCalledWith("start", sent, "queue");
  });

  it("opens the choice on a held Send without sending, and queues from it", () => {
    vi.useFakeTimers();
    const { onSend } = mount({ draft: "after this", busy: true, steers: true });
    hold(steerButton());
    expect(onSend).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("menuitem", { name: /^Queue/ }));
    expect(onSend).toHaveBeenCalledWith("after this", sent, "queue");
  });

  it("sends the default on a tap", () => {
    vi.useFakeTimers();
    const { onSend } = mount({ draft: "now", busy: true, steers: true });
    fireEvent.pointerDown(steerButton(), { button: 0 });
    fireEvent.pointerUp(steerButton());
    fireEvent.click(steerButton());
    act(() => vi.advanceTimersByTime(1000));
    expect(onSend).toHaveBeenCalledWith("now", sent, undefined);
    expect(screen.queryByRole("menuitem")).toBeNull();
  });

  it("does not open the choice on a held Send for a harness that can only queue", () => {
    vi.useFakeTimers();
    const { onSend } = mount({ draft: "later", busy: true, steers: false });
    hold(screen.getByRole("button", { name: "Queue after the running turn" }));
    expect(screen.queryByRole("menuitem")).toBeNull();
    expect(onSend).toHaveBeenCalledWith("later", sent, undefined);
  });

  it("offers the same choice in the send options", async () => {
    const { onSend } = mount({ draft: "go on", busy: true, steers: true });
    fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /^Steer/ }));
    expect(onSend).toHaveBeenCalledWith("go on", sent, undefined);
  });
});

const compact = {
  id: "command:compact",
  name: "compact",
  description: "Compact the transcript",
  kind: "command" as const,
  trigger: "/",
  insertText: "/compact",
  origin: "project" as const,
  behavior: "adapter-action" as const,
  action: "compact",
};

// cmdk scrolls its selected item into view as the menu opens; jsdom has no
// layout to scroll.
beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
});

const catalogue = (over: Partial<ComposerCatalogue> = {}): ComposerCatalogue => ({
  items: [compact],
  loading: false,
  ready: true,
  reload: vi.fn(),
  ...over,
});

describe("a workspace that is still being prepared", () => {
  it("refuses an adapter command picked from the menu while sending is held back", async () => {
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    mount({
      draft: "/comp",
      sendDisabled: true,
      catalogue: catalogue(),
      onRunComposerAction,
    });
    fireEvent.focus(box());
    fireEvent.click(await screen.findByText("/compact"));
    expect(onRunComposerAction).not.toHaveBeenCalled();
  });
});

describe("the command catalogue", () => {
  it("asks for a fresh catalogue once per trigger opening, not once per keystroke", () => {
    const reload = vi.fn();
    const { rerender } = mount({ draft: "", catalogue: catalogue({ reload }) });
    expect(reload).not.toHaveBeenCalled();

    const type = (value: string) => {
      fireEvent.change(box(), { target: { value } });
      rerender({ draft: value, catalogue: catalogue({ reload }) });
    };
    type("/");
    expect(reload).toHaveBeenCalledTimes(1);
    type("/co");
    type("/com");
    expect(reload).toHaveBeenCalledTimes(1);
    type("");
    type("/");
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it("holds back slash text until a catalogue has loaded", () => {
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    const { onSend } = mount({
      draft: "/compact",
      catalogue: catalogue({ items: [], ready: false }),
      onRunComposerAction,
    });
    fireEvent.click(sendButton());
    expect(onSend).not.toHaveBeenCalled();
    expect(onRunComposerAction).not.toHaveBeenCalled();
  });

  it("runs a submitted adapter command instead of sending it as a prompt", async () => {
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    const onDraftChange = vi.fn();
    const { onSend } = mount({ draft: "/compact now", catalogue: catalogue(), onRunComposerAction, onDraftChange });
    fireEvent.click(sendButton());
    await waitFor(() => expect(onDraftChange).toHaveBeenCalledWith(""));
    expect(onRunComposerAction).toHaveBeenCalledWith("compact", "now", "/compact now");
    expect(onSend).not.toHaveBeenCalled();
  });
});

const claude = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: { state: "ready" },
  instances: [
    {
      id: "claude",
      driver: "claude",
      displayName: "Claude Code",
      enabled: true,
      availability: { state: "ready" },
      models: [
        { id: "opus", label: "Opus", default: true, efforts: ["low", "high", "max"] },
        { id: "sonnet", label: "Sonnet", efforts: ["low", "high"] },
        { id: "haiku", label: "Haiku" },
      ],
    },
  ],
} as unknown as HarnessMeta;

describe("the model picker mid-thread", () => {
  beforeAll(() => {
    const proto = window.HTMLElement.prototype;
    proto.hasPointerCapture ??= () => false;
    proto.setPointerCapture ??= () => {};
    proto.releasePointerCapture ??= () => {};
  });

  // A running thread: the switches go to the server, and the props only move
  // when it echoes them, which these tests do by hand.
  const running = (over: Partial<React.ComponentProps<typeof Composer>> = {}) => {
    const onSwitchModel = vi.fn();
    const onSwitchEffort = vi.fn();
    const ref = createRef<ComposerHandle>();
    const view = mount({
      ref,
      harnesses: [claude],
      harness: "claude",
      instance: "claude",
      model: "opus",
      effort: "max",
      onSwitchModel,
      onSwitchEffort,
      ...over,
    });
    return { ...view, ref, onSwitchModel, onSwitchEffort };
  };
  const trigger = () => screen.getByRole("combobox", { name: "Harness and model" });
  const search = () => screen.queryByPlaceholderText(/Search models/);
  const levels = () => within(screen.getByRole("radiogroup", { name: "Reasoning effort" }));
  const choose = async (label: string) =>
    fireEvent.click(await screen.findByRole("option", { name: new RegExp(label) }));

  it("shows the pick before the server echoes it, then follows the server", async () => {
    const { rerender, onSwitchModel, onSwitchEffort } = running();
    fireEvent.click(trigger());
    await choose("Sonnet");

    expect(onSwitchModel).toHaveBeenCalledWith("sonnet");
    // Sonnet has no Max, so the effort drops with it.
    expect(onSwitchEffort).toHaveBeenCalledWith("high");
    // Nothing echoed yet; the open picker already says what was asked for.
    expect(trigger().textContent).toContain("Sonnet");
    expect(levels().queryByRole("radio", { name: "Max" })).toBeNull();
    expect(levels().getByRole("radio", { name: "High" }).getAttribute("aria-checked")).toBe("true");

    rerender({ model: "sonnet" });
    expect(trigger().textContent).toContain("Sonnet");
    expect(trigger().textContent).toContain("High");
    rerender({ model: "sonnet", effort: "high" });

    // Caught up, the pick no longer has a say: a change from elsewhere shows.
    rerender({ model: "opus", effort: "low" });
    expect(trigger().textContent).toContain("Opus");
    expect(trigger().textContent).toContain("Low");
  });

  it("goes back to what is running when the picker closes before the echo", async () => {
    running();
    fireEvent.click(trigger());
    await choose("Sonnet");
    fireEvent.keyDown(search()!, { key: "Escape" });

    expect(search()).toBeNull();
    expect(trigger().textContent).toContain("Opus");
    expect(trigger().textContent).toContain("Max");
  });

  it("drops a pick the server refused while the picker stays open", async () => {
    running({
      onSwitchModel: vi.fn(() => Promise.resolve(false)),
      onSwitchEffort: vi.fn(() => Promise.resolve(true)),
    });
    fireEvent.click(trigger());
    await choose("Sonnet");

    await waitFor(() => expect(trigger().textContent).toContain("Opus"));
    expect(search()).not.toBeNull();
    // Opus's own levels again, Max among them.
    expect(levels().getByRole("radio", { name: "Max" })).toBeTruthy();
  });

  it("opens on the effort level when asked, and moves focus while open", async () => {
    const { ref } = running({ effort: "high" });

    act(() => ref.current!.openModelPicker("effort"));
    await waitFor(() =>
      expect(document.activeElement).toBe(levels().getByRole("radio", { name: "High" })),
    );

    act(() => ref.current!.openModelPicker("model"));
    expect(document.activeElement).toBe(search());
    act(() => ref.current!.openModelPicker("effort"));
    expect(document.activeElement).toBe(levels().getByRole("radio", { name: "High" }));

    // Closed and opened by hand, it starts at the search as it always did.
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(search()).toBeNull();
    fireEvent.click(trigger());
    await waitFor(() => expect(document.activeElement).toBe(search()));
  });

  it("opens on the search when asked for effort on a model with none", async () => {
    const { ref } = running({ model: "haiku", effort: "" });

    act(() => ref.current!.openModelPicker("effort"));

    await waitFor(() => expect(document.activeElement).toBe(search()));
  });
});

describe("Fast across a model switch", () => {
  const codex = {
    id: "codex",
    name: "Codex",
    permissionModes: [],
    availability: { state: "ready" },
    models: [],
    instances: [
      {
        id: "codex",
        driver: "codex",
        displayName: "Codex",
        enabled: true,
        availability: { state: "ready" },
        models: [
          { id: "sol", label: "Sol", default: true, fastTier: "priority" },
          { id: "terra", label: "Terra", fastTier: "turbo" },
          { id: "luna", label: "Luna" },
        ],
      },
    ],
  } as unknown as HarnessMeta;

  function pick(label: string, serviceTier: string) {
    const onSwitchServiceTier = vi.fn();
    mount({
      harnesses: [codex],
      harness: "codex",
      instance: "codex",
      model: "sol",
      serviceTier,
      onSwitchModel: vi.fn(),
      onSwitchServiceTier,
    });
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(screen.getByRole("option", { name: new RegExp(label) }));
    return onSwitchServiceTier;
  }

  beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
  });

  it("stays Fast on the new model's own tier", () => {
    expect(pick("Terra", "priority")).toHaveBeenCalledWith("turbo");
  });

  it("drops to the default on a model with no fast tier", () => {
    expect(pick("Luna", "priority")).toHaveBeenCalledWith("");
  });

  it("leaves a thread that is not Fast alone", () => {
    expect(pick("Terra", "")).not.toHaveBeenCalled();
  });

  function toggleFast(onSwitchServiceTier: (tier: string) => void | Promise<boolean>) {
    mount({
      harnesses: [codex],
      harness: "codex",
      instance: "codex",
      model: "sol",
      serviceTier: "",
      onSwitchModel: vi.fn(),
      onSwitchServiceTier,
    });
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(screen.getByRole("switch", { name: "Fast mode" }));
  }
  const fastSwitch = () => screen.getByRole("switch", { name: "Fast mode" });

  it("shows Fast on before the server echoes it", () => {
    const onSwitchServiceTier = vi.fn();
    toggleFast(onSwitchServiceTier);

    expect(onSwitchServiceTier).toHaveBeenCalledWith("priority");
    expect(fastSwitch().getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain("Fast");
  });

  it("goes back to off when the server refuses it", async () => {
    toggleFast(vi.fn(() => Promise.resolve(false)));

    await waitFor(() => expect(fastSwitch().getAttribute("aria-checked")).toBe("false"));
  });
});
