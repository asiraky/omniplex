// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { FolderBrowser } from "./FolderSources";

// Each request waits until the test answers it, so the order answers land in
// is the test's to choose.
function slowFs() {
  const pending = new Map<string, (res: Response) => void>();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (url: string) =>
        new Promise<Response>((resolve) => {
          pending.set(new URL(url, "http://x").searchParams.get("path")!, resolve);
        }),
    ),
  );
  return async (path: string, body: object | string, status = 200) => {
    const text = typeof body === "string" ? body : JSON.stringify(body);
    await act(async () => pending.get(path)!(new Response(text, { status })));
  };
}

const listing = (path: string, dirs: string[]) => ({ path, parent: "/", dirs });

afterEach(() => vi.unstubAllGlobals());

describe("FolderBrowser", () => {
  it("shows the folder tapped last, whatever order the answers land in", async () => {
    const answer = slowFs();
    render(<FolderBrowser onChoose={() => {}} />);
    await answer("~", listing("/home/me", ["a", "b"]));

    fireEvent.click(screen.getByRole("button", { name: "a" }));
    fireEvent.click(screen.getByRole("button", { name: "b" }));
    await answer("/home/me/b", listing("/home/me/b", ["from-b"]));
    await answer("/home/me/a", listing("/home/me/a", ["from-a"]));

    expect(screen.getByText("/home/me/b")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "from-a" })).toBeNull();
  });

  it("says why a folder would not open", async () => {
    const answer = slowFs();
    render(<FolderBrowser onChoose={() => {}} />);
    await answer("~", "permission denied\n", 403);
    expect(screen.getByText("permission denied")).toBeTruthy();
  });
});
