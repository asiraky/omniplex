import type { Element, Text } from "hast";
import { CheckIcon, CopyIcon } from "lucide-react";
import { gfmFromMarkdown } from "mdast-util-gfm";
import { gfm } from "micromark-extension-gfm";
import { lazy, memo, Suspense, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkBreaks from "remark-breaks";
// Declares the processor data the GFM plugin below writes to.
import type {} from "remark-parse";
import type { Processor } from "unified";

import { useCopy } from "~/lib/clipboard";
import { fileIconFor } from "~/lib/fileIcons";
import { closeOpenFence, splitMarkdownBlocks } from "~/lib/markdownBlocks";
import { useOpenPath } from "~/lib/openPath";
import { detectPath } from "~/lib/paths";
import { cn } from "~/lib/utils";
const MermaidDiagram = lazy(() => import("./MermaidDiagram").then((m) => ({ default: m.MermaidDiagram })));

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
      // 24px to sit in the code block's header; on touch it takes taps
      // across 44px tall and 40px wide, clipped at the block's edge. 40, not
      // 44: in a diagram's header the expand button is 8px to its right.
      className="text-muted-foreground hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring hit-44 relative flex size-6 [--hit-w:40px] shrink-0 items-center justify-center rounded-md transition-colors outline-none focus-visible:ring-2"
    >
      {copied ? <CheckIcon className="text-success size-3" /> : <CopyIcon className="size-3" />}
    </button>
  );
}

// `data-speak-as` tells `~/lib/speech` what a block is when read aloud: the
// rendered DOM alone no longer says it is code, or that it is a diagram.
function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const mermaid = lang.toLowerCase() === "mermaid";
  const source = (
    <div data-speak-as="code-block" className="bg-muted/60 my-2 overflow-hidden rounded-lg border">
      <div className="text-muted-foreground flex items-center gap-2 border-b px-2.5 py-1">
        <span className="min-w-0 flex-1 truncate font-mono text-[10px]">{lang || "text"}</span>
        <CopyButton text={code} />
      </div>
      <pre className="scroll-thin overflow-x-auto overscroll-x-contain p-2.5 font-mono text-[12px] leading-relaxed">
        <code>{code}</code>
      </pre>
    </div>
  );
  return mermaid ? (
    <div data-speak-as="diagram" className="contents">
      <Suspense fallback={source}>
        <MermaidDiagram code={code} copyButton={<CopyButton text={code} />} />
      </Suspense>
    </div>
  ) : source;
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
      data-speak-as="code"
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
  ol: ({ children, start }) => (
    <ol start={start} className="my-2 list-decimal space-y-1 pl-5 first:mt-0 last:mb-0">{children}</ol>
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

/**
 * GitHub flavour, parsing only. remark-gfm also registers the extensions for
 * writing markdown back out, which nothing here does, and that serializer was
 * several KiB of the first load.
 */
function remarkGfm(this: Processor) {
  const data = this.data();
  (data.micromarkExtensions ??= []).push(gfm());
  (data.fromMarkdownExtensions ??= []).push(gfmFromMarkdown());
}

export const PLUGINS = [remarkGfm, remarkBreaks];
// A written document means its single newlines as soft wraps, the way every
// other renderer reads a README; only chat prose relies on them as breaks.
const DOCUMENT_PLUGINS = [remarkGfm];

// One block of chat Markdown. Memoised on its text: once a later block has
// started, this one is finished and keeps its render while the stream grows.
const MarkdownBlock = memo(function MarkdownBlock({ text }: { text: string }) {
  return (
    <ReactMarkdown remarkPlugins={PLUGINS} components={COMPONENTS}>
      {text}
    </ReactMarkdown>
  );
});

/**
 * Markdown as an agent writes it: GitHub flavour, single newlines kept as
 * newlines because model prose relies on them, and raw HTML dropped rather
 * than executed — `rehype-raw` is deliberately absent, so any HTML in the
 * output is text on the page and nothing more.
 *
 * Chat text is cut into blocks, each parsed on its own, so a streaming
 * message re-parses only the block still being written and finished blocks
 * keep their render. They are keyed by position because a stream only ever
 * changes the last one or adds another. Splitting is unconditional, not just
 * while streaming, so the end of a stream costs no full re-parse. The blocks
 * sit directly in the container, with no wrapper each, because `first:` and
 * `last:` spacing and the streaming caret's `:last-child` all need them as
 * siblings. Text whose syntax ties blocks together renders whole.
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
  if (document) {
    return (
      <div className={cn("min-w-0", className)}>
        <ReactMarkdown remarkPlugins={DOCUMENT_PLUGINS} components={COMPONENTS}>
          {text}
        </ReactMarkdown>
      </div>
    );
  }
  // Only the last block can hold an open fence: the splitter never cuts inside one.
  const blocks = splitMarkdownBlocks(text) ?? [text];
  return (
    <div className={cn("min-w-0", className)}>
      {blocks.map((block, i) => (
        <MarkdownBlock key={i} text={i === blocks.length - 1 ? closeOpenFence(block) : block} />
      ))}
    </div>
  );
});
