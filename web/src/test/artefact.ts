import type { Artefact } from "~/lib/artefacts";

/** An artefact as the server sends it, with whatever a test cares about. */
export function makeArtefact(over: Partial<Artefact> = {}): Artefact {
  const name = over.name ?? "report.md";
  return {
    id: "a1",
    name,
    path: `/home/me/Omniplex/proj/${name}`,
    mediaType: "text/markdown",
    size: 10,
    entry: name,
    files: 1,
    modifiedAt: 1000,
    source: "agent",
    shownAt: 1000,
    ...over,
  };
}
