import { MessageCircleQuestionIcon } from "lucide-react";

import { Marker } from "~/components/ui/marker";
import { cn } from "~/lib/utils";
import type { Item, QuestionAnswer, QuestionStatus } from "~/protocol";

import { usePromptFocus } from "./promptFocus";

// What a question that was answered says under it.
function answerText(q: QuestionAnswer) {
  if (q.secret) return "Provided";
  return q.answer ?? "No answer";
}

/**
 * What the agent asked, where it asked it. Pending, the prompt above the
 * composer is where it is answered, and tapping this brings it into view;
 * after, it is the record of what was said. A secret field shows only that
 * something was provided.
 */
export function QuestionRecord({ item }: { item: Item }) {
  const focusPrompt = usePromptFocus();
  const status = (item.status ?? "pending") as QuestionStatus;
  const questions = item.questions ?? [];
  const answered = status === "answered";
  // A bare prompt with no fields has nothing to put under it but that it was answered.
  const bare = questions.length === 1 && questions[0].key === "" && !questions[0].secret && questions[0].answer === undefined;

  const body = (
    <>
      <MessageCircleQuestionIcon aria-hidden className="text-muted-foreground mt-0.5 size-4 shrink-0" />
      <span className="block min-w-0 flex-1 space-y-1.5">
        {questions.map((q) => (
          // Only a bare prompt has no key, and it is then the one entry.
          <span key={q.key} className="block space-y-0.5">
            <span className="block text-[13px] leading-snug text-pretty break-words">{q.question}</span>
            {answered && !bare && (
              <span
                className={cn(
                  "text-muted-foreground block text-[12px] leading-snug break-words whitespace-pre-wrap",
                  (q.secret || q.answer === undefined) && "italic",
                )}
              >
                {answerText(q)}
              </span>
            )}
          </span>
        ))}
        {status !== "answered" || bare ? (
          <span className="flex">
            <Marker tone={status === "pending" ? "attention" : "quiet"}>{STATUS_TEXT[status] ?? status}</Marker>
          </span>
        ) : null}
      </span>
    </>
  );

  const shell = "fade-in bg-card/70 flex w-full max-w-md items-start gap-2.5 rounded-xl border px-3 py-2.5 text-left";
  if (status === "pending" && focusPrompt) {
    return (
      <button
        type="button"
        onClick={() => focusPrompt("question")}
        className={cn(shell, "hover:bg-accent/40 focus-visible:ring-ring cursor-pointer transition-colors outline-none focus-visible:ring-2")}
      >
        {body}
      </button>
    );
  }
  return <div className={shell}>{body}</div>;
}

const STATUS_TEXT: Record<QuestionStatus, string> = {
  pending: "Waiting for your answer",
  answered: "Answered",
  declined: "Declined",
  cancelled: "Cancelled",
};
