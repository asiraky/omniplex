// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { NewProject } from "./NewProject";
import type { Project } from "~/protocol";

const made = { id: "p9", name: "Bowerbird", defaults: {}, folders: [], createdAt: 0, updatedAt: 0 } satisfies Project;

function open(over: Partial<React.ComponentProps<typeof NewProject>> = {}) {
  const props = {
    onCreate: vi.fn(async () => made),
    listRepos: vi.fn(async () => [
      { name: "asiraky/omniplex", description: "harness driver" },
      { name: "asiraky/dotfiles", private: true },
    ]),
    onClose: vi.fn(),
    ...over,
  } satisfies React.ComponentProps<typeof NewProject>;
  render(<NewProject {...props} />);
  return props;
}

describe("new project", () => {
  it("needs only a name", async () => {
    const props = open();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "  Bowerbird " } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(props.onCreate).toHaveBeenCalledWith({ name: "Bowerbird" }));
    await waitFor(() => expect(props.onClose).toHaveBeenCalled());
  });

  // The list is only asked for when someone opens it: it is a gh process on
  // the server and most new projects never need it.
  it("clones a repo picked from the list, filtered by what was typed", async () => {
    const props = open();
    expect(props.listRepos).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    await waitFor(() => expect(screen.getByText("asiraky/dotfiles")).toBeTruthy());

    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "omni" } });
    expect(screen.queryByText("asiraky/dotfiles")).toBeNull();
    fireEvent.click(screen.getByText("asiraky/omniplex"));

    await waitFor(() =>
      expect(props.onCreate).toHaveBeenCalledWith({ url: "asiraky/omniplex", name: undefined }),
    );
  });

  it("clones what was pasted when it is not in the list", async () => {
    const props = open({ listRepos: vi.fn(async () => { throw new Error("gh is not signed in"); }) });
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    await waitFor(() => expect(screen.getByText("gh is not signed in")).toBeTruthy());
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "git@example.com:team/site.git" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Clone" }));
    await waitFor(() =>
      expect(props.onCreate).toHaveBeenCalledWith({ url: "git@example.com:team/site.git", name: undefined }),
    );
  });

  it("stays open with the reason when the server refuses", async () => {
    const props = open({
      onCreate: vi.fn(async () => {
        throw new Error("repository not found. Check the URL");
      }),
    });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(screen.getByText(/repository not found/)).toBeTruthy());
    expect(props.onClose).not.toHaveBeenCalled();
  });
});
