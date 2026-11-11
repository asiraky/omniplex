// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { chunk } from "~/lib/chunk";
import { render } from "~/test/harness";

import { OnDemand } from "./Overlays";

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function Real({ open }: Props) {
  return open ? <div role="dialog" aria-label="Real" /> : null;
}

/** An import the test settles by hand, once per attempt. */
function heldImport() {
  const attempts: { resolve: (c: typeof Real) => void; reject: (e: Error) => void }[] = [];
  const importer = vi.fn(
    () =>
      new Promise<typeof Real>((resolve, reject) => {
        attempts.push({ resolve, reject });
      }),
  );
  return { c: chunk(importer), importer, attempts };
}

function Host({ c, onOpenChange }: { c: ReturnType<typeof heldImport>["c"]; onOpenChange?: (o: boolean) => void }) {
  const [open, setOpen] = useState(false);
  const change = (o: boolean) => {
    onOpenChange?.(o);
    setOpen(o);
  };
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        open
      </button>
      <OnDemand chunk={c} props={{ open, onOpenChange: change }} title="Loading" />
    </>
  );
}

const placeholder = () => screen.queryByRole("dialog", { name: "Loading" });

describe("OnDemand", () => {
  it("fetches nothing until first opened", () => {
    const { c, importer } = heldImport();
    render(<Host c={c} />);
    expect(importer).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("holds focus and Escape in a placeholder until the code arrives", async () => {
    const { c, attempts } = heldImport();
    const onOpenChange = vi.fn();
    render(<Host c={c} onOpenChange={onOpenChange} />);
    fireEvent.click(screen.getByText("open"));
    expect(placeholder()).toBeTruthy();
    expect(placeholder()!.contains(document.activeElement)).toBe(true);

    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(onOpenChange).toHaveBeenCalledWith(false);

    fireEvent.click(screen.getByText("open"));
    await act(async () => attempts[0].resolve(Real));
    expect(placeholder()).toBeNull();
    expect(screen.getByRole("dialog", { name: "Real" })).toBeTruthy();
  });

  it("says when the fetch failed, and tries again on the next opening", async () => {
    const { c, importer, attempts } = heldImport();
    render(<Host c={c} />);
    fireEvent.click(screen.getByText("open"));
    await act(async () => attempts[0].reject(new Error("offline")));
    expect(screen.getByText(/Couldn't load/)).toBeTruthy();

    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    fireEvent.click(screen.getByText("open"));
    expect(importer).toHaveBeenCalledTimes(2);
    expect(screen.queryByText(/Couldn't load/)).toBeNull();
    await act(async () => attempts[1].resolve(Real));
    expect(screen.getByRole("dialog", { name: "Real" })).toBeTruthy();
  });
});
