// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, type Mock } from "vitest";

import { render } from "~/test/harness";
import type { Card, CardDestination } from "~/protocol";

import type { ResolveCard } from "./CardFrame";
import PendingCard from "./PendingCard";

function show(card: Card, resolve: Mock<ResolveCard> = vi.fn(async () => ({}))) {
  render(<PendingCard request={{ requestId: "r1", prompt: "Proposal", schema: {}, card }} resolve={resolve} />);
  return resolve;
}

const save = (name = "Save") => fireEvent.click(screen.getByRole("button", { name }));

const mcp = (over: Partial<Card> = {}): Card => ({
  kind: "add_mcp_server",
  projectId: "p1",
  projectName: "Omniplex",
  scope: "project",
  server: {
    name: "linear",
    url: "https://mcp.linear.app/mcp",
    env: [{ name: "LINEAR_KEY", held: true }],
    headers: [{ name: "X_TEAM", held: false }],
  },
  ...over,
});

const destinations: CardDestination[] = [
  { kind: "project", folder: "/home/p1", label: "This project" },
  { kind: "personal", folder: "", label: "Personal" },
];

describe("an MCP server card", () => {
  it("accepts with no edits when nothing was touched", async () => {
    const resolve = show(mcp());
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", undefined));
  });

  it("keeps the held secret when Replace is opened and left empty", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("button", { name: "Replace LINEAR_KEY" }));
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", undefined));
  });

  it("sends a typed replacement and a typed empty value, trimmed", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("button", { name: "Replace LINEAR_KEY" }));
    fireEvent.change(screen.getByLabelText("LINEAR_KEY"), { target: { value: "lin_123\n" } });
    fireEvent.change(screen.getByLabelText("X_TEAM"), { target: { value: " eng " } });
    save();
    await waitFor(() =>
      expect(resolve).toHaveBeenCalledWith("accept", { env: { LINEAR_KEY: "lin_123" }, headers: { X_TEAM: "eng" } }),
    );
  });

  it("drops a replacement the user undid", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("button", { name: "Replace LINEAR_KEY" }));
    fireEvent.change(screen.getByLabelText("LINEAR_KEY"), { target: { value: "lin_123" } });
    fireEvent.click(screen.getByRole("button", { name: "Keep the agent's LINEAR_KEY" }));
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", undefined));
  });

  it("sends the scope only once it differs from the proposal", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("radio", { name: "Everywhere" }));
    fireEvent.click(screen.getByRole("radio", { name: "Omniplex" }));
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", undefined));
  });

  it("sends a scope the user switched to", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("radio", { name: "Everywhere" }));
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", { scope: "everywhere" }));
  });

  it("has no scope switch in a thread without a project", () => {
    show(mcp({ projectId: "", projectName: "" }));
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.queryByRole("radio")).toBeNull();
  });
});

describe("answering a card", () => {
  it("keeps a card whose answer failed, says why, and lets the user try again", async () => {
    const resolve = vi.fn().mockRejectedValueOnce(new Error("LINEAR_KEY is required")).mockResolvedValue({});
    show(mcp(), resolve);

    save();
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", "LINEAR_KEY is required");
    expect(screen.getByRole("button", { name: "Save" })).toHaveProperty("disabled", false);
    expect(screen.getByRole("button", { name: "Decline" })).toHaveProperty("disabled", false);

    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("locks the card once the answer is taken, until it is taken down", async () => {
    show(mcp());
    save();
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toHaveProperty("disabled", true));
    expect(screen.getByRole("button", { name: "Decline" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Replace LINEAR_KEY" })).toHaveProperty("disabled", true);
  });

  it("sends one answer for a double tap", async () => {
    let finish!: () => void;
    const resolve = vi.fn(() => new Promise<void>((r) => (finish = r)));
    show(mcp(), resolve);
    const button = screen.getByRole("button", { name: "Save" });
    fireEvent.click(button);
    fireEvent.click(button);
    await act(async () => finish());
    expect(resolve).toHaveBeenCalledTimes(1);
  });

  it("answers nothing when folded and opened again", async () => {
    const resolve = show(mcp());
    fireEvent.click(screen.getByRole("button", { expanded: true }));
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    await act(async () => {});
    expect(resolve).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Save" })).toHaveProperty("disabled", false);
  });

  it("declines without edits, whatever was typed", async () => {
    const resolve = show(mcp());
    fireEvent.change(screen.getByLabelText("X_TEAM"), { target: { value: "eng" } });
    fireEvent.click(screen.getByRole("button", { name: "Decline" }));
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("decline", undefined));
  });
});

describe("a skill install card", () => {
  const install = (): Card => ({
    kind: "install_skill",
    projectId: "p1",
    staged: {
      id: "stg_1",
      source: "acme/skills",
      skills: [
        { name: "a", description: "", files: [{ path: "SKILL.md", size: 1 }], picked: true },
        // Go sends an empty slice as null.
        { name: "b", description: "", files: null as unknown as [] },
      ],
    },
    destination: "/home/p1",
    destinations,
  });

  it("installs what was proposed with no edits", async () => {
    const resolve = show(install());
    save("Install");
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", undefined));
  });

  it("sends the skills ticked and the destination picked", async () => {
    const resolve = show(install());
    fireEvent.click(screen.getByRole("checkbox", { name: "Install b" }));
    fireEvent.click(screen.getByRole("radio", { name: /Personal/ }));
    save("Install 2");
    await waitFor(() =>
      expect(resolve).toHaveBeenCalledWith("accept", { skills: ["a", "b"], destination: "" }),
    );
  });

  it("cannot install nothing", () => {
    show(install());
    fireEvent.click(screen.getByRole("checkbox", { name: "Install a" }));
    expect(screen.getByRole("button", { name: "Install" })).toHaveProperty("disabled", true);
  });
});

describe("a new skill card", () => {
  const create = (): Card => ({
    kind: "create_skill",
    projectId: "p1",
    skill: { name: "x", description: "d", content: "---\nname: x\n---\nbody" },
    destination: "/home/p1",
    destinations,
  });

  it("sends the destination only when it moved", async () => {
    const resolve = show(create());
    fireEvent.click(screen.getByRole("radio", { name: /Personal/ }));
    save("Install");
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", { destination: "" }));
  });
});

describe("a sign-in card", () => {
  it("sends the definition fields the user edited", async () => {
    const resolve = show({
      kind: "add_sign_in",
      cli: { id: "gws", name: "Google Workspace", statusCommand: "gws status", signInCommand: "gws login", accounts: ["work"] },
    });
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "gws auth status" } });
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", { cli: { statusCommand: "gws auth status" } }));
  });
});

describe("an account card", () => {
  const account: Card = { kind: "add_account", account: { cli: "gws", cliName: "Google Workspace", name: "work" } };

  it("sends a rename", async () => {
    const resolve = show(account);
    fireEvent.change(screen.getByLabelText("Account"), { target: { value: " home " } });
    save();
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", { name: "home" }));
  });

  it("cannot save a blank name", () => {
    show(account);
    fireEvent.change(screen.getByLabelText("Account"), { target: { value: "  " } });
    expect(screen.getByRole("button", { name: "Save" })).toHaveProperty("disabled", true);
  });
});
