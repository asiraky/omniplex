// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Item, QuestionAnswer } from "~/protocol";
import { render } from "~/test/harness";

import { PromptFocusContext } from "./promptFocus";
import { QuestionRecord } from "./QuestionRecord";

function question(status: string, questions: QuestionAnswer[]): Item {
  return { id: "question:r1", kind: "question", status: status as Item["status"], questions };
}

function renderWithFocus(item: Item) {
  const focusPrompt = vi.fn();
  render(
    <PromptFocusContext.Provider value={focusPrompt}>
      <QuestionRecord item={item} />
    </PromptFocusContext.Provider>,
  );
  return focusPrompt;
}

const two: QuestionAnswer[] = [
  { key: "db", question: "Which database?" },
  { key: "why", question: "Why that one?" },
];

describe("a question record", () => {
  it("waits for an answer, and takes the reader to the prompt on tap", () => {
    const focusPrompt = renderWithFocus(question("pending", two));
    expect(screen.getByText("Which database?")).toBeTruthy();
    expect(screen.getByText("Why that one?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Waiting for your answer/ }));
    expect(focusPrompt).toHaveBeenCalledWith("question");
  });

  it("waits without being a control when there is no prompt to go to", () => {
    render(<QuestionRecord item={question("pending", two)} />);
    expect(screen.getByText("Waiting for your answer")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows each answer under its question, and says which went unanswered", () => {
    renderWithFocus(question("answered", [{ ...two[0], answer: "Postgres" }, two[1]]));
    const db = screen.getByText("Which database?").parentElement!;
    expect(db.textContent).toBe("Which database?Postgres");
    expect(screen.getByText("Why that one?").parentElement!.textContent).toBe("Why that one?No answer");
    expect(screen.queryByText("Waiting for your answer")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("says a secret was provided and never shows it", () => {
    renderWithFocus(
      question("answered", [{ key: "token", question: "API token", secret: true, answer: "sk-live-123" }]),
    );
    expect(screen.getByText("Provided")).toBeTruthy();
    expect(document.body.textContent).not.toContain("sk-live-123");
  });

  it("says only that a bare prompt was answered", () => {
    renderWithFocus(question("answered", [{ key: "", question: "Carry on?" }]));
    expect(screen.getByText("Answered")).toBeTruthy();
    expect(screen.queryByText("No answer")).toBeNull();
  });

  it("shows a bare prompt's answer when it has one", () => {
    renderWithFocus(question("answered", [{ key: "", question: "Name it", answer: "omni" }]));
    expect(screen.getByText("omni")).toBeTruthy();
    expect(screen.queryByText("Answered")).toBeNull();
  });

  it.each([
    ["declined", "Declined"],
    ["cancelled", "Cancelled"],
  ])("says it was %s, with no answers", (status, label) => {
    renderWithFocus(question(status, two));
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.queryByText("No answer")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
