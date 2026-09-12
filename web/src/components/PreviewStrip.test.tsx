// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { render } from "~/test/harness";
import { PreviewStrip, previewName } from "./PreviewStrip";
import type { Preview } from "~/protocol";

function preview(over: Partial<Preview> = {}): Preview {
  return {
    id: "web-inbox",
    port: 5050,
    label: "web",
    scheme: "http",
    source: "process",
    url: "https://web-inbox.agent.example.net",
    ...over,
  };
}

describe("PreviewStrip", () => {
  it("renders nothing when no service is running", () => {
    render(<PreviewStrip previews={[]} />);
    expect(screen.queryByRole("link")).toBeNull();
  });

  // The link goes to the server, not to the service. The server picks the
  // address this device can actually reach and mints the ticket; a client
  // that linked straight to preview.url would break the published case.
  it("links through the server so the ticket is minted", () => {
    render(<PreviewStrip previews={[preview()]} />);
    const link = screen.getByRole("link");
    expect(link.getAttribute("href")).toBe("/api/previews/web-inbox/open");
    expect(link.getAttribute("target")).toBe("_blank");
  });

  it("escapes ids in the link", () => {
    render(<PreviewStrip previews={[preview({ id: "a b" })]} />);
    expect(screen.getByRole("link").getAttribute("href")).toBe("/api/previews/a%20b/open");
  });

  it("shows every running service", () => {
    render(
      <PreviewStrip
        previews={[preview(), preview({ id: "api-inbox", port: 8080, label: "api" })]}
      />,
    );
    expect(screen.getAllByRole("link")).toHaveLength(2);
  });

  // The address is worth showing somewhere, but not at the cost of a chip
  // wide enough to push the composer around on a phone.
  it("keeps the address in the title rather than the chip", () => {
    render(<PreviewStrip previews={[preview()]} />);
    expect(screen.getByRole("link").title).toContain("https://web-inbox.agent.example.net");
  });
});

describe("previewName", () => {
  it("trusts a name the project declared", () => {
    expect(previewName(preview({ source: "declared", label: "app" }))).toBe("app");
  });

  // A port we merely noticed should say so plainly: claiming a name we
  // guessed would be worse than admitting we only know the number.
  it("falls back to the port for anything we only noticed", () => {
    expect(previewName(preview({ source: "process", label: "" }))).toBe("port 5050");
    expect(previewName(preview({ source: "process", label: "5050" }))).toBe("port 5050");
  });

  it("keeps a detected container's name alongside its port", () => {
    expect(previewName(preview({ source: "docker", label: "mailhog", port: 8025 }))).toBe(
      "mailhog · 8025",
    );
  });
});
