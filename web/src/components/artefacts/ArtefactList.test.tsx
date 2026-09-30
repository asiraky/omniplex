// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ArtefactList } from "~/components/artefacts/ArtefactList";
import { makeArtefact } from "~/test/artefact";
import { render } from "~/test/harness";

const art = (id: string, source: "agent" | "upload", shownAt: number) =>
  makeArtefact({ id, name: `${id}.txt`, mediaType: "text/plain", source, shownAt });

const names = () => screen.getAllByRole("button", { name: /\.txt/ }).map((b) => b.textContent?.match(/(old|revised|upload)\.txt/)?.[0]);

describe("ArtefactList", () => {
  const list = [
    art("old", "agent", 100),
    // Uploaded first, then revised and shown by the agent: newest, and the
    // agent's.
    art("revised", "agent", 300),
    art("upload", "upload", 200),
  ];

  it("lists the most recently shown first and filters by who showed it", () => {
    render(<ArtefactList artefacts={list} onOpen={vi.fn()} />);
    expect(names()).toEqual(["revised.txt", "upload.txt", "old.txt"]);
    fireEvent.click(screen.getByRole("radio", { name: "Uploads" }));
    expect(names()).toEqual(["upload.txt"]);
    fireEvent.click(screen.getByRole("radio", { name: "Agent" }));
    expect(names()).toEqual(["revised.txt", "old.txt"]);
  });

  it("opens the one tapped", () => {
    const onOpen = vi.fn();
    render(<ArtefactList artefacts={list} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: /upload\.txt/ }));
    expect(onOpen).toHaveBeenCalledWith("upload");
  });
});
