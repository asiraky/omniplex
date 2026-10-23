import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";

import { Markdown } from "~/components/Markdown";
import { speakableText } from "~/lib/speech";

/**
 * Markdown that is shown as plain text, read aloud as if it were rendered: a
 * prompt with a pasted fence or table reads the same way a reply would. It is
 * rendered off-screen and synchronously, because the click that asked for it
 * has to start the audio itself or a phone will not play it.
 */
export function speakableMarkdown(md: string): string {
  const host = document.createElement("div");
  const root = createRoot(host);
  try {
    flushSync(() => root.render(<Markdown text={md} />));
    return speakableText(host);
  } finally {
    root.unmount();
  }
}
