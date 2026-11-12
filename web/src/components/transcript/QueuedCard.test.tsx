// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { QueuedPrompt } from "~/protocol";
import { render } from "~/test/harness";

import { QueuedCard } from "./QueuedCard";

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

const TRAILER =
  "\n\n<attached-files>\n- notes.md (text/markdown, 1 KB, artefact a1): /tmp/notes.md\n</attached-files>";

function card(queued: Partial<QueuedPrompt>, over: Partial<Parameters<typeof QueuedCard>[0]> = {}) {
  const props = {
    queued: { queueId: "q1", prompt: "do the thing", ...queued },
    threadId: "t",
    artefacts: [],
    busy: false,
    steers: false,
    onDequeue: vi.fn(),
    onEdit: vi.fn().mockResolvedValue(true),
    onSend: vi.fn(),
    ...over,
  };
  render(<QueuedCard {...props} />);
  return props;
}

const editor = () => screen.getByRole("textbox", { name: "Edit queued prompt" });

describe("editing a queued prompt", () => {
  it("opens on a tap with the words alone and saves them with the files block intact", async () => {
    const { onEdit } = card({ prompt: `read this${TRAILER}` });
    fireEvent.click(screen.getByRole("button", { name: "read this" }));
    expect(editor()).toHaveProperty("value", "read this");
    fireEvent.change(editor(), { target: { value: "read this twice" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onEdit).toHaveBeenCalledWith("q1", `read this twice${TRAILER}`);
    await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
  });

  it("saves on Enter and keeps Shift+Enter for a newline", () => {
    const { onEdit } = card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "changed" } });
    fireEvent.keyDown(editor(), { key: "Enter", shiftKey: true });
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.keyDown(editor(), { key: "Enter" });
    expect(onEdit).toHaveBeenCalledWith("q1", "changed");
  });

  it("leaves off the newlines a touch keyboard's Enter put at the end", () => {
    const { onEdit } = card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "changed\n\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onEdit).toHaveBeenCalledWith("q1", "changed");
  });

  it("makes no round trip when all that changed was a trailing newline", async () => {
    const { onEdit } = card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "do the thing\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
    expect(onEdit).not.toHaveBeenCalled();
  });

  it("puts the old words back on Escape", () => {
    const { onEdit } = card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "changed" } });
    fireEvent.keyDown(editor(), { key: "Escape" });
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByRole("button", { name: "do the thing" })).toBeTruthy();
  });

  it("closes without a round trip when nothing changed", async () => {
    const { onEdit } = card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
    expect(onEdit).not.toHaveBeenCalled();
  });

  it("will not save an empty prompt", () => {
    card({});
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "  " } });
    expect(screen.getByRole("button", { name: "Save" })).toHaveProperty("disabled", true);
  });

  it("sends just the files block when the words are cleared from a prompt with a file", () => {
    const { onEdit } = card({ prompt: `see attached${TRAILER}` });
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onEdit).toHaveBeenCalledWith("q1", TRAILER);
  });

  it("stays open with the edit in it when the server refuses", async () => {
    const { onEdit } = card({}, { onEdit: vi.fn().mockResolvedValue(false) });
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(editor(), { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onEdit).toHaveBeenCalled());
    await waitFor(() => expect(editor()).toHaveProperty("disabled", false));
    expect(editor()).toHaveProperty("value", "changed");
  });

  it("cannot edit what the harness already holds", () => {
    card({ sent: true });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "do the thing" })).toBeNull();
  });
});

describe("sending a queued prompt early", () => {
  it("steers it in mid-turn when the harness can take it", () => {
    const { onSend } = card({}, { busy: true, steers: true });
    fireEvent.click(screen.getByRole("button", { name: "Steer now" }));
    expect(onSend).toHaveBeenCalledWith("q1");
    expect(screen.queryByRole("button", { name: "Send now" })).toBeNull();
  });

  it("offers nothing mid-turn when the harness cannot steer", () => {
    card({}, { busy: true, steers: false });
    expect(screen.queryByRole("button", { name: "Steer now" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send now" })).toBeNull();
  });

  it("sends it straight away when the thread is idle, held or not", () => {
    const { onSend } = card({ held: true }, { busy: false, steers: false });
    fireEvent.click(screen.getByRole("button", { name: "Send now" }));
    expect(onSend).toHaveBeenCalledWith("q1");
  });
});
