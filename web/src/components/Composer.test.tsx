// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { createRef, useRef, useState } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { useKeybindings } from "~/app/useKeybindings";
import { stubExecCommand } from "~/test/execCommand";
import { render, wrap } from "~/test/harness";
import { Composer, type ComposerHandle, type ComposerStash } from "./Composer";
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

  it("pastes spreadsheet cells as their text, not the picture that came with them", () => {
    const { onAttachFiles } = mount();
    const cells = "Q1\t1200\nQ2\t1350";
    const clipboardData = { ...text(cells), files: [png("image.png")], types: ["Files", "text/plain"] };
    // Not handled: the textarea's own paste puts the text in.
    expect(fireEvent.paste(box(), { clipboardData })).toBe(true);
    expect(onAttachFiles).not.toHaveBeenCalled();
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

  it("sends while an image is still going up, leaving the wait to the thread", () => {
    const { onSend } = mount({
      draft: "look at this",
      attachments: [staged({ status: "uploading", id: undefined })],
    });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("look at this", { imageIds: [], files: [] }, undefined);
  });

  it("sends a message that is nothing but an image still going up", () => {
    const { onSend } = mount({ attachments: [staged({ status: "uploading", id: undefined })] });
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).toHaveBeenCalledTimes(1);
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

  it("sends while a file is still going up, and shows how far it has got", () => {
    const { onSend } = mount({
      draft: "read this",
      attachments: [stagedFile({ status: "uploading", artefactId: undefined, progress: 0.4 })],
    });
    expect(screen.getByRole("progressbar", { name: "Uploading b.pdf" }).getAttribute("aria-valuenow")).toBe("40");
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["image", staged({ status: "error", id: undefined, error: "network down" })],
    ["file", stagedFile({ status: "error", artefactId: undefined, error: "network down" })],
  ])("retries a failed %s from its chip", (_, failed) => {
    const onRetryAttachment = vi.fn();
    const { onRemoveAttachment } = mount({ attachments: [failed], onRetryAttachment });
    fireEvent.click(screen.getByRole("button", { name: `Retry ${failed.name}` }));
    expect(onRetryAttachment).toHaveBeenCalledWith(failed.key);
    // Removing it is still an option for a file that will never go.
    fireEvent.click(screen.getByRole("button", { name: `Remove ${failed.name}` }));
    expect(onRemoveAttachment).toHaveBeenCalledWith(failed.key);
  });

  it("offers no retry on a chip that has not failed, or with nowhere to retry it", () => {
    const { rerender } = mount({
      attachments: [staged({ status: "uploading", id: undefined }), stagedFile()],
      onRetryAttachment: vi.fn(),
    });
    expect(screen.queryByRole("button", { name: /^Retry/ })).toBeNull();
    rerender({
      attachments: [staged({ status: "error", id: undefined, error: "network down" })],
      onRetryAttachment: undefined,
    });
    expect(screen.queryByRole("button", { name: /^Retry/ })).toBeNull();
  });

  it("holds the next message back while one waits for its uploads, but keeps it writable", () => {
    const onDraftChange = vi.fn();
    const { onSend, rerender } = mount({ draft: "and another thing", waiting: true, onDraftChange });
    expect(screen.getByRole("status")).toBeTruthy();
    expect(sendButton()).toHaveProperty("disabled", true);
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).not.toHaveBeenCalled();
    fireEvent.change(box(), { target: { value: "and another thing too" } });
    expect(onDraftChange).toHaveBeenCalledWith("and another thing too");

    rerender({ draft: "and another thing", waiting: false });
    expect(screen.queryByRole("status")).toBeNull();
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(onSend).toHaveBeenCalledTimes(1);
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

describe("a message that came back unsent", () => {
  it("sends it again from the note", () => {
    const onRetry = vi.fn();
    mount({ failure: { reason: "offline", onRetry, onDismiss: vi.fn() } });
    fireEvent.click(within(screen.getByRole("alert")).getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("offers no retry for one that cannot go again as it was, only dismissal", () => {
    const onDismiss = vi.fn();
    mount({ failure: { reason: "a file did not upload", onDismiss } });
    const note = screen.getByRole("alert");
    expect(within(note).queryByRole("button", { name: "Retry" })).toBeNull();
    fireEvent.click(within(note).getByRole("button", { name: "Dismiss" }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("will not send it again while another message waits on its uploads", () => {
    mount({ waiting: true, failure: { reason: "offline", onRetry: vi.fn(), onDismiss: vi.fn() } });
    expect(within(screen.getByRole("alert")).getByRole("button", { name: "Retry" })).toHaveProperty(
      "disabled",
      true,
    );
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

  it("will not schedule while an image is still uploading, which a schedule would drop", async () => {
    const onSchedule = vi.fn();
    const { rerender } = mount({
      draft: "look later",
      onSchedule,
      attachments: [staged({ status: "uploading", id: undefined })],
    });
    openOptions();
    const item = await screen.findByRole("menuitem", { name: /Schedule send/ });
    expect(item.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(item);
    expect(onSchedule).not.toHaveBeenCalled();

    rerender({ draft: "look later", onSchedule, attachments: [staged()] });
    const ready = await screen.findByRole("menuitem", { name: /Schedule send/ });
    expect(ready.getAttribute("aria-disabled")).toBeNull();
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

  it("sends text handed to it as though it were typed, a command included", async () => {
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    const ref = createRef<ComposerHandle>();
    const { onSend } = mount({ ref, catalogue: catalogue(), onRunComposerAction });
    act(() => ref.current?.send("/compact now"));
    await waitFor(() => expect(onRunComposerAction).toHaveBeenCalledWith("compact", "now", "/compact now"));
    expect(onSend).not.toHaveBeenCalled();
    act(() => ref.current?.send(" run the tests "));
    expect(onSend).toHaveBeenCalledWith("run the tests", { imageIds: [], files: [] }, undefined);
  });
});

// Picks are typed in through execCommand so the browser can undo them.
describe("typing a pick in where undo can take it back", () => {
  const review = {
    id: "skill:review",
    name: "review",
    kind: "skill" as const,
    trigger: "/",
    insertText: "/review",
    origin: "project" as const,
    behavior: "prompt" as const,
  };
  const exec = stubExecCommand();

  // The draft has to follow onDraftChange, as it does in the app: a fixed
  // draft prop would have React put the old value straight back.
  function renderControlled(initial: string, over: Partial<React.ComponentProps<typeof Composer>> = {}) {
    const onDraftChange = vi.fn();
    const ref = createRef<ComposerHandle>();
    function Host() {
      const [draft, setDraft] = useState(initial);
      return (
        <Composer
          ref={ref}
          draft={draft}
          onDraftChange={(next) => {
            onDraftChange(next);
            setDraft(next);
          }}
          disabled={false}
          busy={false}
          onSend={vi.fn()}
          onCancel={vi.fn()}
          catalogue={catalogue({ items: [compact, review] })}
          {...over}
        />
      );
    }
    render(<Host />);
    return { onDraftChange, ref };
  }

  it("types a picked command over the slash token", async () => {
    const edit = exec.editing();
    const { onDraftChange } = renderControlled("/rev");
    fireEvent.focus(box());
    fireEvent.click(await screen.findByText("/review"));

    expect(edit).toHaveBeenCalledWith("insertText", false, "/review ");
    expect(onDraftChange.mock.calls).toEqual([["/review "]]);
    expect(box()).toHaveProperty("value", "/review ");
  });

  it("writes the pick into the draft when the browser cannot type it", async () => {
    exec.absent();
    const { onDraftChange } = renderControlled("/rev");
    fireEvent.focus(box());
    fireEvent.click(await screen.findByText("/review"));

    expect(onDraftChange.mock.calls).toEqual([["/review "]]);
  });

  it("clears an adapter command that finishes after the textarea was left, without taking focus back", async () => {
    const edit = exec.editing();
    let finish = () => {};
    const onRunComposerAction = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    const { onDraftChange } = renderControlled("/comp", { onRunComposerAction });
    fireEvent.focus(box());
    fireEvent.click(await screen.findByText("/compact"));

    const elsewhere = screen.getByRole("button", { name: "Send" });
    elsewhere.focus();
    await act(async () => finish());

    expect(onDraftChange).toHaveBeenCalledWith("");
    expect(edit).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(elsewhere);
  });

  it("types at the end of the draft through the handle", () => {
    const edit = exec.editing();
    const { onDraftChange, ref } = renderControlled("fix the bug");

    let typed = false;
    act(() => {
      typed = ref.current!.typeAtEnd(" /review ");
    });

    expect(typed).toBe(true);
    expect(edit).toHaveBeenCalledWith("insertText", false, " /review ");
    expect(onDraftChange.mock.calls).toEqual([["fix the bug /review "]]);
  });

  it("leaves the draft to the caller when the handle cannot type", () => {
    exec.absent();
    const { onDraftChange, ref } = renderControlled("fix the bug");

    expect(ref.current!.typeAtEnd(" /review ")).toBe(false);
    expect(onDraftChange).not.toHaveBeenCalled();
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

describe("fitting the toolbar to the composer's width", () => {
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
        models: [{ id: "sol", label: "Sol", default: true, efforts: ["low", "high"], fastTier: "priority" }],
      },
    ],
  } as unknown as HarnessMeta;

  // jsdom lays nothing out, so every box is as wide as this says.
  const boxWidth = (px: number) => vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(px);
  afterEach(() => vi.restoreAllMocks());

  const mountAt = (px: number) => {
    boxWidth(px);
    mount({
      harnesses: [codex],
      harness: "codex",
      instance: "codex",
      model: "sol",
      effort: "high",
      serviceTier: "priority",
      tools: (place) => <button type="button">{`tool in ${place}`}</button>,
    });
    return screen.getByRole("combobox", { name: "Harness and model" });
  };

  it("names only the model, and moves the tools under the box, at phone width", () => {
    const trigger = mountAt(358);
    expect(trigger.textContent).toContain("Sol");
    expect(trigger.textContent).not.toContain("High");
    const drawer = screen.getByRole("group", { name: "Thread options" });
    expect(within(drawer).getByRole("button", { name: "tool in drawer" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "tool in row" })).toBeNull();
  });

  it("gives the model its whole name on a line of its own when the box is too narrow to share one", () => {
    const trigger = mountAt(236);
    expect(trigger.textContent).toContain("Sol");
    expect(trigger.textContent).toContain("High");
    expect(screen.getByRole("button", { name: "tool in drawer" })).toBeTruthy();
  });

  it("names the effort again once there is room, with the tools still under the box", () => {
    const trigger = mountAt(480);
    expect(trigger.textContent).toContain("High");
    expect(screen.getByRole("button", { name: "tool in drawer" })).toBeTruthy();
  });

  it("keeps everything in the toolbar when there is room", () => {
    const trigger = mountAt(700);
    expect(trigger.textContent).toContain("High");
    expect(screen.getByRole("button", { name: "tool in row" })).toBeTruthy();
    expect(screen.queryByRole("group", { name: "Thread options" })).toBeNull();
  });

  it("assumes room for everything before the box has been measured", () => {
    const trigger = mountAt(0);
    expect(trigger.textContent).toContain("High");
    expect(screen.getByRole("button", { name: "tool in row" })).toBeTruthy();
  });
});

describe("earlier prompts and stashed drafts", () => {
  const exec = stubExecCommand();
  const history = ["first ask", "second ask", "third ask"];

  function renderControlled(initial: string, over: Partial<React.ComponentProps<typeof Composer>> = {}) {
    const onDraftChange = vi.fn();
    function Host() {
      const [draft, setDraft] = useState(initial);
      return (
        <Composer
          draft={draft}
          onDraftChange={(next) => {
            onDraftChange(next);
            setDraft(next);
          }}
          disabled={false}
          busy={false}
          onSend={vi.fn()}
          onCancel={vi.fn()}
          history={history}
          {...over}
        />
      );
    }
    render(<Host />);
    return { onDraftChange };
  }

  const up = () => fireEvent.keyDown(box(), { key: "ArrowUp" });
  const down = () => fireEvent.keyDown(box(), { key: "ArrowDown" });
  const value = () => (box() as HTMLTextAreaElement).value;

  it("walks back through the thread's prompts from an empty composer, and forward to empty again", () => {
    const edit = exec.editing();
    renderControlled("");
    up();
    expect(value()).toBe("third ask");
    up();
    up();
    expect(value()).toBe("first ask");
    up();
    expect(value()).toBe("first ask");
    down();
    expect(value()).toBe("second ask");
    down();
    down();
    expect(value()).toBe("");
    // Typed in, so undo can take each step back.
    expect(edit).toHaveBeenCalledWith("insertText", false, "third ask");
  });

  it("leaves a draft alone, and a recall once it is edited", () => {
    exec.editing();
    renderControlled("my own words");
    up();
    expect(value()).toBe("my own words");

    fireEvent.change(box(), { target: { value: "" } });
    up();
    fireEvent.change(box(), { target: { value: "third ask, edited" } });
    up();
    expect(value()).toBe("third ask, edited");
  });

  it("moves the caret through a prompt of several lines before stepping past it", () => {
    exec.editing();
    renderControlled("", { history: ["one", "line a\nline b"] });
    up();
    expect(value()).toBe("line a\nline b");
    // The caret ends after "line b", on the last line, so ArrowUp is the caret's.
    const caretMove = fireEvent.keyDown(box(), { key: "ArrowUp" });
    expect(caretMove).toBe(true);
    expect(value()).toBe("line a\nline b");
    (box() as HTMLTextAreaElement).setSelectionRange(2, 2);
    up();
    expect(value()).toBe("one");
  });

  it("keeps walking back past a recalled command instead of opening the command menu on it", () => {
    exec.editing();
    renderControlled("", { history: ["/older", "/newer"] });
    up();
    expect(value()).toBe("/newer");
    expect(screen.queryByRole("listbox")).toBeNull();
    up();
    expect(value()).toBe("/older");
  });

  it("writes the recall when the browser cannot type it", () => {
    exec.absent();
    const { onDraftChange } = renderControlled("");
    up();
    expect(onDraftChange.mock.calls).toEqual([["third ask"]]);
  });

  const stash = (over: Partial<ComposerStash> = {}) => ({
    entries: [] as ComposerStash["entries"],
    save: vi.fn(() => true),
    take: vi.fn(),
    drop: vi.fn(),
    ...over,
  });
  const openMenu = () =>
    fireEvent.pointerDown(screen.getByRole("button", { name: "Stash and recent prompts" }), {
      button: 0,
      ctrlKey: false,
    });

  it("has no menu without a stash", () => {
    renderControlled("");
    expect(screen.queryByRole("button", { name: "Stash and recent prompts" })).toBeNull();
  });

  it("stashes the draft, and only once there is something to stash", async () => {
    const s = stash();
    const { rerender } = mount({ draft: "", stash: s });
    openMenu();
    expect((await screen.findByRole("menuitem", { name: "Stash draft" })).getAttribute("data-disabled")).toBe("");
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });

    rerender({ draft: "half a thought" });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Stash draft" }));
    expect(s.save).toHaveBeenCalled();
  });

  it("puts a stashed draft back, and removes one without putting it back", async () => {
    const entries = [
      { id: "a", text: "parked one", attachments: [], at: 1 },
      { id: "b", text: "parked two", attachments: [staged()], at: 0 },
    ];
    const s = stash({ entries });
    mount({ stash: s });
    openMenu();
    const two = await screen.findByRole("menuitem", { name: /parked two/ });
    expect(two.textContent).toContain("1 attachment");
    fireEvent.click(within(two).getByRole("button", { name: "Remove stashed draft" }));
    expect(s.drop).toHaveBeenCalledWith("b");
    expect(s.take).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("menuitem", { name: /parked one/ }));
    expect(s.take).toHaveBeenCalledWith("a");
  });

  it("removes the stashed draft under the keyboard with Delete", async () => {
    const s = stash({ entries: [{ id: "a", text: "parked one", attachments: [], at: 1 }] });
    mount({ stash: s });
    openMenu();
    fireEvent.keyDown(await screen.findByRole("menuitem", { name: /parked one/ }), { key: "Delete" });
    expect(s.drop).toHaveBeenCalledWith("a");
    expect(s.take).not.toHaveBeenCalled();
  });

  it("recalls a recent prompt from the menu, newest first, parking the draft it replaces", async () => {
    exec.editing();
    const s = stash();
    const { onDraftChange } = renderControlled("unsent words", { stash: s });
    openMenu();
    const items = await screen.findAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual(["Stash draft", "third ask", "second ask", "first ask"]);

    fireEvent.click(screen.getByRole("menuitem", { name: "first ask" }));
    // Only the words: any attachments stay to go out with the recalled prompt.
    expect(s.save).toHaveBeenCalledWith(true);
    expect(onDraftChange).toHaveBeenLastCalledWith("first ask");
  });

  it("does not park a prompt already sent when another is recalled over it", async () => {
    exec.editing();
    const s = stash();
    const { onDraftChange } = renderControlled("second ask", { stash: s });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "first ask" }));
    expect(s.save).not.toHaveBeenCalled();
    expect(onDraftChange).toHaveBeenLastCalledWith("first ask");
  });

  it("recalls into an empty composer without parking anything", async () => {
    exec.editing();
    const s = stash();
    const { onDraftChange } = renderControlled("", { stash: s });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "second ask" }));
    expect(s.save).not.toHaveBeenCalled();
    expect(onDraftChange).toHaveBeenLastCalledWith("second ask");
  });
});

describe("sending and starting a new thread", () => {
  const sent = { imageIds: [], files: [] };
  const hold = (el: HTMLElement) => {
    fireEvent.pointerDown(el, { button: 0 });
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(el);
    fireEvent.click(el);
  };
  const sendAndNewItem = () => screen.getByRole("menuitem", { name: /^Send and start a new thread/ });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("opens a menu on a held Send in an idle thread, and sends before starting the new one", () => {
    vi.useFakeTimers();
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ draft: "fix the flaky test", onSendAndNewThread });
    hold(sendButton());
    expect(onSend).not.toHaveBeenCalled();
    fireEvent.click(sendAndNewItem());
    expect(onSend).toHaveBeenCalledWith("fix the flaky test", sent, undefined);
    expect(onSendAndNewThread).toHaveBeenCalledTimes(1);
    expect(onSend.mock.invocationCallOrder[0]).toBeLessThan(onSendAndNewThread.mock.invocationCallOrder[0]!);
  });

  it("offers a plain send in the held menu, which stays in the thread", () => {
    vi.useFakeTimers();
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ draft: "just this", onSendAndNewThread });
    hold(sendButton());
    fireEvent.click(screen.getByRole("menuitem", { name: "Send" }));
    expect(onSend).toHaveBeenCalledWith("just this", sent, undefined);
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("keeps the steer-or-queue choice beside it mid-turn", () => {
    vi.useFakeTimers();
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ draft: "after this", busy: true, steers: true, onSendAndNewThread });
    hold(screen.getByRole("button", { name: "Steer the running turn" }));
    expect(screen.getByRole("menuitem", { name: /^Queue/ })).toBeTruthy();
    fireEvent.click(sendAndNewItem());
    expect(onSend).toHaveBeenCalledWith("after this", sent, undefined);
    expect(onSendAndNewThread).toHaveBeenCalledTimes(1);
  });

  it("sends on a held Send, with no menu, where there is no new thread to start", () => {
    vi.useFakeTimers();
    const { onSend } = mount({ draft: "hi" });
    hold(sendButton());
    expect(screen.queryByRole("menuitem")).toBeNull();
    expect(onSend).toHaveBeenCalledWith("hi", sent, undefined);
  });

  it("does not open on a held Send with nothing to send", () => {
    vi.useFakeTimers();
    const onSendAndNewThread = vi.fn();
    mount({ draft: "", onSendAndNewThread });
    hold(sendButton());
    expect(screen.queryByRole("menuitem")).toBeNull();
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("closes the held menu once the message can no longer go", () => {
    vi.useFakeTimers();
    const onSendAndNewThread = vi.fn();
    const { rerender } = mount({ draft: "soon", onSendAndNewThread });
    hold(sendButton());
    expect(sendAndNewItem()).toBeTruthy();
    rerender({ draft: "soon", onSendAndNewThread, waiting: true });
    expect(screen.queryByRole("menuitem")).toBeNull();
  });

  it("offers it in the send options, which it alone is enough to show", async () => {
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ draft: "ship it", onSendAndNewThread });
    fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /^Send and start a new thread/ }));
    expect(onSend).toHaveBeenCalledWith("ship it", sent, undefined);
    expect(onSendAndNewThread).toHaveBeenCalledTimes(1);
  });

  it("sends through the handle the way the keyboard does", () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ ref, draft: "next task", busy: true, steers: true, onSendAndNewThread });
    act(() => ref.current!.sendAndNewThread());
    expect(onSend).toHaveBeenCalledWith("next task", sent, undefined);
    expect(onSendAndNewThread).toHaveBeenCalledTimes(1);
  });

  it("does nothing through the handle where there is no new thread to start", () => {
    const ref = createRef<ComposerHandle>();
    const { onSend } = mount({ ref, draft: "stay here" });
    act(() => ref.current!.sendAndNewThread());
    expect(onSend).not.toHaveBeenCalled();
  });

  it.each([
    ["an empty draft", { draft: "  " }],
    ["the thread closed", { draft: "hi", disabled: true }],
    ["the workspace still being prepared", { draft: "hi", sendDisabled: true }],
    ["another message waiting on its uploads", { draft: "hi", waiting: true }],
    ["slash text before the catalogue loads", { draft: "/compact", catalogue: catalogue({ ready: false }) }],
  ])("stays in the thread when the message is held back: %s", (_, over) => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    const { onSend } = mount({ ref, onSendAndNewThread, ...over });
    act(() => ref.current!.sendAndNewThread());
    expect(onSend).not.toHaveBeenCalled();
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("starts the new thread once an adapter command has run", async () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    mount({ ref, draft: "/compact", catalogue: catalogue(), onRunComposerAction, onSendAndNewThread });
    act(() => ref.current!.sendAndNewThread());
    await waitFor(() => expect(onSendAndNewThread).toHaveBeenCalledTimes(1));
    expect(onRunComposerAction).toHaveBeenCalledWith("compact", "", "/compact");
  });

  it("stays in the thread when the draft was rewritten while the adapter command ran", async () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    let finish!: () => void;
    const onRunComposerAction = vi.fn(() => new Promise<void>((r) => (finish = r)));
    const over = { ref, catalogue: catalogue(), onRunComposerAction, onSendAndNewThread };
    const { rerender } = mount({ ...over, draft: "/compact" });
    act(() => ref.current!.sendAndNewThread());
    rerender({ ...over, draft: "the next thing" });
    await act(async () => finish());
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("stays put when the composer is gone before the adapter command returns", async () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    let finish!: () => void;
    const onRunComposerAction = vi.fn(() => new Promise<void>((r) => (finish = r)));
    mount({ ref, draft: "/compact", catalogue: catalogue(), onRunComposerAction, onSendAndNewThread });
    act(() => ref.current!.sendAndNewThread());
    cleanup();
    await act(async () => finish());
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("stays in the thread for a client command, which sends nothing", () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    const onRunClientAction = vi.fn();
    const usage = { ...compact, id: "command:usage", name: "usage", insertText: "/usage", behavior: "client-action" as const, action: "usage" };
    mount({ ref, draft: "/usage", catalogue: catalogue({ items: [usage] }), onRunClientAction, onSendAndNewThread });
    act(() => ref.current!.sendAndNewThread());
    expect(onRunClientAction).toHaveBeenCalledWith("usage");
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });

  it("stays in the thread when the adapter command fails", async () => {
    const ref = createRef<ComposerHandle>();
    const onSendAndNewThread = vi.fn();
    const onRunComposerAction = vi.fn().mockRejectedValue(new Error("no"));
    mount({ ref, draft: "/compact", catalogue: catalogue(), onRunComposerAction, onSendAndNewThread });
    await act(async () => ref.current!.sendAndNewThread());
    expect(onRunComposerAction).toHaveBeenCalled();
    expect(onSendAndNewThread).not.toHaveBeenCalled();
  });
});

describe("⇧⌘↵ from an open thread's composer", () => {
  const app = { paletteOpen: false, helpOpen: false, threadOpen: true, turnRunning: false, panelOpen: false };

  function Keyed(props: React.ComponentProps<typeof Composer>) {
    const ref = useRef<ComposerHandle>(null);
    useKeybindings({ sendAndNewThread: () => ref.current?.sendAndNewThread() }, app);
    return <Composer ref={ref} {...props} />;
  }

  it("sends and starts a new thread, claiming the key before the textarea makes a newline of it", () => {
    const onSend = vi.fn();
    const onSendAndNewThread = vi.fn();
    render(
      <Keyed
        draft="write the migration"
        onDraftChange={vi.fn()}
        disabled={false}
        busy={false}
        onSend={onSend}
        onCancel={vi.fn()}
        onSendAndNewThread={onSendAndNewThread}
      />,
    );
    box().focus();
    // jsdom reports no platform, so this is the Ctrl+Shift+↵ of Windows and Linux.
    const went = fireEvent.keyDown(box(), { key: "Enter", code: "Enter", ctrlKey: true, shiftKey: true });
    expect(went).toBe(false);
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith("write the migration", { imageIds: [], files: [] }, undefined);
    expect(onSendAndNewThread).toHaveBeenCalledTimes(1);
  });
});
