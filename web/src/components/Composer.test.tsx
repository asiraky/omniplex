// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import { Composer } from "./Composer";
import type { Attachment } from "~/lib/attachments";
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
    expect(onSend).toHaveBeenCalledWith("", { imageIds: ["img-1"], files: [] });
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
    });
  });

  it("sends a message that is nothing but a file", () => {
    const { onSend } = mount({ attachments: [stagedFile()] });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("", { imageIds: [], files: [{ artefactId: "art-1" }] });
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
    expect(onSend).toHaveBeenCalledWith("ship it", { imageIds: [], files: [] });
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
