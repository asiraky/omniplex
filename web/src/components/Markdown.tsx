import type { Element, Text } from "hast";
import { CheckIcon, CopyIcon } from "lucide-react";
import { memo, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";

import { useCopy } from "~/lib/clipboard";
import { fileIconFor } from "~/lib/fileIcons";
import { useOpenPath } from "~/lib/openPath";
import { detectPath } from "~/lib/paths";
import { cn } from "~/lib/utils";

// Text arrives a character at a time, so at any moment the parser is being
// handed a document that is very likely mid-sentence. Most half-written
// constructs are harmless — an unfinished `**bold` is just literal text until
// its closing pair lands. A fence is the exception: an opening ``` with no
// close makes every line after it a paragraph, then reflows the lot into a
// code block the instant the close arrives. Closing it here means the block is
// a code block from its first line and simply grows.
function closeOpenFence(text: string): string {
  let open: { char: string; len: number } | null = null;
  for (const line of text.split("\n")) {
    const m = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (!m) continue;
    const [, fence, rest] = m;
    const char = fence[0];
    if (open === null) {
      // A backtick fence's info string cannot itself contain a backtick, so
      // "``` a ` b" opens nothing.
      if (char === "`" && rest.includes("`")) continue;
      open = { char, len: fence.length };
      continue;
    }
    // A closing fence is the same character, at least as long as the opener,
    // and carries no info string — "```js" inside a ```` block is content.
    if (char === open.char && fence.length >= open.len && rest.trim() === "") open = null;
  }
  return open === null ? text : `${text}\n${open.char.repeat(open.len)}`;
}

// The fenced text as the author wrote it, straight off the syntax tree — the
// rendered children would have lost the newlines that make it copyable.
function codeText(node: Element | undefined): string {
  const code = node?.children.find((c): c is Element => c.type === "element" && c.tagName === "code");
  const raw = (code ?? node)?.children
    .filter((c): c is Text => c.type === "text")
    .map((c) => c.value)
    .join("");
  return (raw ?? "").replace(/\n$/, "");
}

function codeLang(node: Element | undefined): string {
  const code = node?.children.find((c): c is Element => c.type === "element" && c.tagName === "code");
  const classes = code?.properties?.className;
  const list = Array.isArray(classes) ? classes.map(String) : [];
  return list.find((c) => c.startsWith("language-"))?.slice("language-".length) ?? "";
}

function CopyButton({ text }: { text: string }) {
  const { copied, copy } = useCopy();

  return (
    <button
      type="button"
      onClick={() => void copy(text)}
      aria-label={copied ? "Copied" : "Copy code"}
      className="text-muted-foreground hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring flex size-6 shrink-0 items-center justify-center rounded-md transition-colors outline-none focus-visible:ring-2"
    >
      {copied ? <CheckIcon className="text-success size-3" /> : <CopyIcon className="size-3" />}
    </button>
  );
}

function CodeBlock({ code, lang }: { code: string; lang: string }) {
  return (
    <div className="bg-muted/60 my-2 overflow-hidden rounded-lg border">
      <div className="text-muted-foreground flex items-center gap-2 border-b px-2.5 py-1">
        <span className="min-w-0 flex-1 truncate font-mono text-[10px]">{lang || "text"}</span>
        <CopyButton text={code} />
      </div>
      <pre className="scroll-thin overflow-x-auto overscroll-x-contain p-2.5 font-mono text-[12px] leading-relaxed">
        <code>{code}</code>
      </pre>
    </div>
  );
}

/**
 * Inline code, upgraded to a clickable file chip when it reads as a path and
 * something has offered to open one. Agents backtick paths nearly always, so
 * intercepting here covers most of it without walking arbitrary text — and a
 * span that arrives a character at a time simply flips from code to chip the
 * moment it qualifies, which is a styling change, not a reflow.
 */
function InlineCode({ children }: { children?: ReactNode }) {
  const open = useOpenPath();
  const text = typeof children === "string" ? children : undefined;
  const detected = open && text ? detectPath(text) : null;

  if (!detected) {
    return <code className="bg-muted rounded px-1 py-0.5 font-mono text-[0.9em]">{children}</code>;
  }

  // The chip has to read as the same size as the inline `code` beside it —
  // same 0.9em mono, same px-1/py-0.5 box — while an inline-flex box, unlike a
  // plain inline one, does contribute its own height to the line. The negative
  // vertical margin cancels exactly the padding it adds, so the chip looks the
  // same as inline code but never pushes lines apart. `leading-[normal]` (the
  // font's own metrics) rather than a fixed ratio keeps the box hugging the
  // glyphs the way inline code's content area does.
  const { Icon, tone } = fileIconFor(detected.path);
  return (
    <button
      type="button"
      onClick={() => open!(detected.path, detected.line)}
      title={`Open ${detected.path}`}
      className="bg-muted hover:bg-accent focus-visible:ring-ring -my-0.5 inline-flex max-w-full cursor-pointer items-baseline gap-1 rounded px-1 py-0.5 align-baseline font-mono text-[0.9em] leading-[normal] shadow-[inset_0_0_0_1px_var(--color-border)] transition-colors outline-none focus-visible:ring-2"
    >
      <Icon className={cn("size-[0.85em] shrink-0 self-center", tone)} />
      <span className="truncate">{text}</span>
    </button>
  );
}

// Block spacing is set on the elements themselves rather than on the container
// so a message that is one paragraph — which most are — has no leading or
// trailing gap of its own to fight with the transcript's own rhythm.
const COMPONENTS: Components = {
  p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
  h1: ({ children }) => <h1 className="mt-4 mb-2 text-[17px] font-semibold first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="mt-4 mb-2 text-[16px] font-semibold first:mt-0">{children}</h2>,
  h3: ({ children }) => <h3 className="mt-3 mb-1.5 text-[15px] font-semibold first:mt-0">{children}</h3>,
  h4: ({ children }) => <h4 className="mt-3 mb-1.5 font-semibold first:mt-0">{children}</h4>,
  h5: ({ children }) => <h5 className="mt-3 mb-1.5 font-semibold first:mt-0">{children}</h5>,
  h6: ({ children }) => (
    <h6 className="text-muted-foreground mt-3 mb-1.5 font-semibold first:mt-0">{children}</h6>
  ),
  ul: ({ children }) => <ul className="my-2 list-disc space-y-1 pl-5 first:mt-0 last:mb-0">{children}</ul>,
  ol: ({ children }) => (
    <ol className="my-2 list-decimal space-y-1 pl-5 first:mt-0 last:mb-0">{children}</ol>
  ),
  li: ({ children }) => <li className="[&>ul]:my-1 [&>ol]:my-1">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="text-muted-foreground my-2 border-l-2 pl-3 first:mt-0 last:mb-0">
      {children}
    </blockquote>
  ),
  hr: () => <hr className="my-3" />,
  a: ({ href, children }) => (
    <a
      href={href}
      // Model output is untrusted: a new tab must not be handed a window
      // reference back into this one.
      target="_blank"
      rel="noopener noreferrer nofollow"
      className="text-primary underline underline-offset-2"
    >
      {children}
    </a>
  ),
  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  em: ({ children }) => <em className="italic">{children}</em>,
  // Only inline code reaches this: fenced blocks are taken by `pre` below and
  // rendered without descending into their children.
  code: ({ children }) => <InlineCode>{children}</InlineCode>,
  pre: ({ node }) => <CodeBlock code={codeText(node)} lang={codeLang(node)} />,
  table: ({ children }) => (
    <div className="scroll-thin scroll-shadow-x my-2 overflow-x-auto overscroll-x-contain rounded-lg border first:mt-0 last:mb-0">
      <table className="w-full border-collapse text-[13px]">{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className="bg-muted/60 border-b px-2.5 py-1.5 text-left font-semibold">{children}</th>
  ),
  td: ({ children }) => <td className="border-b px-2.5 py-1.5 align-top last:border-b-0">{children}</td>,
  // An image tag in model output is a fetch the reader never asked for: it
  // would leak their address and timing to whoever wrote the URL, and reach
  // hosts only this browser can see. The link is offered instead, so loading
  // it stays the reader's decision.
  img: ({ src, alt }) =>
    typeof src === "string" ? (
      <a
        href={src}
        target="_blank"
        rel="noopener noreferrer nofollow"
        className="text-primary underline underline-offset-2"
      >
        {alt || "image"}
      </a>
    ) : (
      <span className="text-muted-foreground">{alt || "image"}</span>
    ),
};

const PLUGINS = [remarkGfm, remarkBreaks];
// A written document means its single newlines as soft wraps, the way every
// other renderer reads a README; only chat prose relies on them as breaks.
const DOCUMENT_PLUGINS = [remarkGfm];

/**
 * Markdown as an agent writes it: GitHub flavour, single newlines kept as
 * newlines because model prose relies on them, and raw HTML dropped rather
 * than executed — `rehype-raw` is deliberately absent, so any HTML in the
 * output is text on the page and nothing more.
 *
 * Memoised on the text, so a message that has stopped changing is not
 * re-parsed every time a later one grows.
 *
 * `document` is for a whole file rather than a chat message: complete, so no
 * fence needs closing, and written with soft-wrapped lines.
 */
export const Markdown = memo(function Markdown({
  text,
  className,
  document = false,
}: {
  text: string;
  className?: string;
  document?: boolean;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <ReactMarkdown remarkPlugins={document ? DOCUMENT_PLUGINS : PLUGINS} components={COMPONENTS}>
        {document ? text : closeOpenFence(text)}
      </ReactMarkdown>
    </div>
  );
});
