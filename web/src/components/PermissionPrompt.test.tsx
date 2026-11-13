// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { ElicitationPrompt } from "./ElicitationPrompt";
import { PermissionPrompt } from "./PermissionPrompt";
import type { PendingElicitation, PendingPermission } from "~/protocol";

const permission: PendingPermission = {
  requestId: "r1",
  toolCallId: "t1",
  toolName: "Bash",
  title: "Run `ls`",
  input: { command: "ls" },
  options: [
    { optionId: "yes", name: "Allow", kind: "allow_once" },
    { optionId: "no", name: "Deny", kind: "reject_once" },
  ] as PendingPermission["options"],
};

const elicitation: PendingElicitation = {
  requestId: "r2",
  prompt: "Paste your token",
  schema: { type: "object", properties: { token: { type: "string" } } },
};

// These prompts float over the scrolling transcript on a wrapper that paints
// no background of its own (App.tsx). Any alpha on their own surface lets the
// transcript show through the one band that has to be unambiguously readable,
// so the opacity is asserted rather than left to the eye.
function surface(element: HTMLElement) {
  return element.className.split(/\s+/);
}

describe("blocking prompts are opaque", () => {
  it("paints the permission band with no alpha", () => {
    render(<PermissionPrompt request={permission} onResolve={vi.fn()} />);

    const band = screen.getByRole("group", { name: "Permission request" });
    expect(surface(band)).toContain("bg-card");
    expect(band.className).not.toMatch(/bg-card\//);
  });

  it("paints the tool-input detail with no alpha", () => {
    render(<PermissionPrompt request={permission} onResolve={vi.fn()} />);

    const detail = screen.getByText(/"command"/);
    expect(surface(detail)).toContain("bg-muted");
    expect(detail.className).not.toMatch(/bg-muted\//);
  });

  it("paints the elicitation band the same way", () => {
    render(<ElicitationPrompt threadId="t-1" request={elicitation} onResolve={vi.fn()} />);

    const band = screen.getByRole("form", { name: "Input requested" });
    expect(surface(band)).toContain("bg-card");
    expect(band.className).not.toMatch(/bg-card\//);
  });
});
