// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { TitleEditor } from "./TitleEditor";
import { render } from "~/test/harness";

function renderEditor(title = "Old name") {
  const onSave = vi.fn();
  const onDone = vi.fn();
  render(<TitleEditor title={title} label="Thread title" onSave={onSave} onDone={onDone} />);
  return { onSave, onDone, field: screen.getByRole("textbox", { name: "Thread title" }) };
}

describe("TitleEditor", () => {
  it("mounts focused with the old name selected, ready to be typed over", () => {
    const { field } = renderEditor();
    const input = field as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, "Old name".length]);
  });

  it("saves on Enter, once, even though leaving the field blurs it", () => {
    const { field, onSave, onDone } = renderEditor();
    fireEvent.change(field, { target: { value: "New name" } });
    fireEvent.keyDown(field, { key: "Enter" });
    fireEvent.blur(field);
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("New name");
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("saves when the user clicks away", () => {
    const { field, onSave, onDone } = renderEditor();
    fireEvent.change(field, { target: { value: "New name" } });
    fireEvent.blur(field);
    expect(onSave).toHaveBeenCalledWith("New name");
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("keeps the old name on Escape", () => {
    const { field, onSave, onDone } = renderEditor();
    fireEvent.change(field, { target: { value: "New name" } });
    fireEvent.keyDown(field, { key: "Escape" });
    fireEvent.blur(field);
    expect(onSave).not.toHaveBeenCalled();
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("does not save a field cleared to nothing", () => {
    const { field, onSave, onDone } = renderEditor();
    fireEvent.change(field, { target: { value: "  " } });
    fireEvent.keyDown(field, { key: "Enter" });
    expect(onSave).not.toHaveBeenCalled();
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("does not finish while an IME is still composing", () => {
    const { field, onSave, onDone } = renderEditor();
    fireEvent.keyDown(field, { key: "Enter", isComposing: true });
    expect(onSave).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
  });
});
