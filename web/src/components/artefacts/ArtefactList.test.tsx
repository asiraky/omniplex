// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ArtefactList } from "~/components/artefacts/ArtefactList";
import type { Artefact } from "~/lib/artefacts";
import { render } from "~/test/harness";

const art = (id: string, sources: ("agent" | "upload")[], times: number[]): Artefact => ({
  id,
  name: `${id}.txt`,
  versions: sources.map((source, i) => ({
    version: i + 1,
    mediaType: "text/plain",
    size: 10,
    entry: `${id}.txt`,
    files: 1,
    source,
    publishedAt: times[i]!,
  })),
});

const names = () => screen.getAllByRole("button", { name: /\.txt/ }).map((b) => b.textContent?.match(/(old|revised|upload)\.txt/)?.[0]);

describe("ArtefactList", () => {
  const list = [
    art("old", ["agent"], [100]),
    // Uploaded first, revised by the agent last: newest, and the agent's.
    art("revised", ["upload", "agent"], [50, 300]),
    art("upload", ["upload"], [200]),
  ];

  it("lists newest first by latest version and filters by who made the latest", () => {
    render(<ArtefactList artefacts={list} onOpen={vi.fn()} />);
    expect(names()).toEqual(["revised.txt", "upload.txt", "old.txt"]);
    fireEvent.click(screen.getByRole("radio", { name: "Uploads" }));
    expect(names()).toEqual(["upload.txt"]);
    fireEvent.click(screen.getByRole("radio", { name: "Agent" }));
    expect(names()).toEqual(["revised.txt", "old.txt"]);
  });

  it("opens the one tapped at its latest", () => {
    const onOpen = vi.fn();
    render(<ArtefactList artefacts={list} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: /upload\.txt/ }));
    expect(onOpen).toHaveBeenCalledWith("upload");
  });
});
