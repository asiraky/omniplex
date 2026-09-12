// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import { Composer } from "./Composer";
import type { Attachment } from "~/lib/attachments";

const png = (name = "shot.png") => new File([new Uint8Array([1, 2, 3])], name, { type: "image/png" });

// jsdom has no DataTransfer worth using; the composer only ever asks a drop
// for its files and its types, so that is all a test has to hand it.
const transfer = (files: File[]) => ({ files, types: ["Files"] });

const staged = (over: Partial<Attachment> = {}): Attachment => ({
  key: "k1",
  name: "shot.png",
  previewUrl: "blob:preview",
  status: "ready",
  id: "img-1",
  ...over,
});

function mount(over: Partial<React.ComponentProps<typeof Composer>> = {}) {
  const onSend = vi.fn();
  const onAttachImages = vi.fn();
  const onRemoveAttachment = vi.fn();
  const view = render(
    <Composer
      draft=""
      onDraftChange={vi.fn()}
      disabled={false}
      busy={false}
      onSend={onSend}
      onCancel={vi.fn()}
      onAttachImages={onAttachImages}
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
          onAttachImages={onAttachImages}
          onRemoveAttachment={onRemoveAttachment}
          {...over}
          {...next}
        />,
      ),
    );
  return { onSend, onAttachImages, onRemoveAttachment, rerender };
}

const fileInput = () => document.querySelector<HTMLInputElement>("input[type=file]")!;
// Drop and paste are handled on the card around the textarea; React events
// bubble, so firing on the box a hand would actually be over is enough.
const box = () => screen.getByLabelText("Message");
const sendButton = () => screen.getByRole("button", { name: "Send" });

describe("attaching images", () => {
  it("takes a picked file and clears the input so the same file can be picked twice", () => {
    const { onAttachImages } = mount();
    const file = png();
    fireEvent.change(fileInput(), { target: { files: [file] } });
    expect(onAttachImages).toHaveBeenCalledWith([file]);
    expect(fileInput().value).toBe("");
  });

  it("takes a dropped image", () => {
    const { onAttachImages } = mount();
    const file = png();
    fireEvent.drop(box(), { dataTransfer: transfer([file]) });
    expect(onAttachImages).toHaveBeenCalledWith([file]);
  });

  it("takes a pasted screenshot and leaves pasted text to the textarea", () => {
    const { onAttachImages } = mount();
    fireEvent.paste(box(), { clipboardData: { files: [], types: ["text/plain"] } });
    expect(onAttachImages).not.toHaveBeenCalled();

    const file = png("clipboard.png");
    fireEvent.paste(box(), { clipboardData: { files: [file], types: ["Files"] } });
    expect(onAttachImages).toHaveBeenCalledWith([file]);
  });

  it("ignores a drop that carries no files", () => {
    const { onAttachImages } = mount();
    fireEvent.drop(box(), { dataTransfer: { files: [], types: ["text/uri-list"] } });
    expect(onAttachImages).not.toHaveBeenCalled();
  });

  it("attaches nothing while the composer is disabled", () => {
    const { onAttachImages } = mount({ disabled: true, disabledPlaceholder: "Reconnecting" });
    fireEvent.change(fileInput(), { target: { files: [png()] } });
    expect(onAttachImages).not.toHaveBeenCalled();
  });
});

describe("sending with images", () => {
  it("sends a message that is nothing but pictures", () => {
    const { onSend } = mount({ attachments: [staged()] });
    fireEvent.click(sendButton());
    expect(onSend).toHaveBeenCalledWith("", "now");
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

  it("removes a staged image", () => {
    const { onRemoveAttachment } = mount({ attachments: [staged()] });
    fireEvent.click(screen.getByRole("button", { name: "Remove shot.png" }));
    expect(onRemoveAttachment).toHaveBeenCalledWith("k1");
  });
});

describe("a workspace that is still being prepared", () => {
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

  it("refuses an adapter command picked from the menu while sending is held back", async () => {
    const onRunComposerAction = vi.fn().mockResolvedValue(undefined);
    mount({
      draft: "/comp",
      sendDisabled: true,
      loadComposerItems: async () => [compact],
      onRunComposerAction,
    });
    fireEvent.focus(box());
    fireEvent.click(await screen.findByText("/compact"));
    expect(onRunComposerAction).not.toHaveBeenCalled();
  });

  it("reloads the command catalogue once the workspace can take commands", async () => {
    // The first load fails the way the actor fails before it has a session.
    const loadComposerItems = vi
      .fn()
      .mockRejectedValueOnce(new Error("workspace is not ready"))
      .mockResolvedValue([compact]);
    const { rerender } = mount({ draft: "", sendDisabled: true, loadComposerItems });
    await waitFor(() => expect(loadComposerItems).toHaveBeenCalledTimes(1));
    await act(async () => rerender({ sendDisabled: false }));
    await waitFor(() => expect(loadComposerItems).toHaveBeenCalledTimes(2));
  });
});

describe("how a message is delivered", () => {
  const busy = { busy: true, draft: "and also fix the tests", sessionId: "s1" };
  const send = () => screen.getAllByRole("button", { name: /^Send to the running turn/ }).pop()!;
  // Radix menus open on pointerdown, not click.
  const open = (name: RegExp | string) =>
    fireEvent.pointerDown(screen.getByRole("button", { name }), { button: 0, ctrlKey: false });

  it("sends with the default delivery until one is picked", () => {
    const { onSend } = mount(busy);
    fireEvent.click(send());
    expect(onSend).toHaveBeenCalledWith("and also fix the tests", "now");
  });

  it("sends with the picked delivery and remembers it for the session", async () => {
    const { onSend } = mount(busy);
    open(/^Delivery:/);
    fireEvent.click(await screen.findByText("Interrupt"));
    fireEvent.click(send());
    expect(onSend).toHaveBeenCalledWith("and also fix the tests", "interrupt");

    // A remount is a page reload, or coming back to this session later: the
    // choice belongs to the session, not to this component instance.
    const second = mount(busy);
    fireEvent.click(send());
    expect(second.onSend).toHaveBeenCalledWith("and also fix the tests", "interrupt");
  });

  it("offers no delivery choice while the session is idle", () => {
    mount({ ...busy, busy: false });
    expect(screen.queryByRole("button", { name: /^Delivery:/ })).toBeNull();
  });
});

describe("the permission mode chip", () => {
  const modes = [
    { id: "default", label: "Ask", description: "Ask before every edit", default: true },
    { id: "acceptEdits", label: "Accept edits", description: "Edits go through" },
  ];

  it("switches mode without restarting the session", async () => {
    const onSwitchMode = vi.fn();
    mount({ modes, mode: "default", onSwitchMode });
    fireEvent.pointerDown(screen.getByRole("button", { name: "Permission mode: Ask" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(await screen.findByText("Accept edits"));
    expect(onSwitchMode).toHaveBeenCalledWith("acceptEdits");
  });

  it("stays away when the harness has no modes to offer", () => {
    mount({ modes: [], onSwitchMode: vi.fn() });
    expect(screen.queryByRole("button", { name: /^Permission mode/ })).toBeNull();
  });
});
