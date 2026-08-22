// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
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
  render(
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
  return { onSend, onAttachImages, onRemoveAttachment };
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
    expect(onSend).toHaveBeenCalledWith("");
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

  it("removes a staged image", () => {
    const { onRemoveAttachment } = mount({ attachments: [staged()] });
    fireEvent.click(screen.getByRole("button", { name: "Remove shot.png" }));
    expect(onRemoveAttachment).toHaveBeenCalledWith("k1");
  });
});
