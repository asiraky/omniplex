import { gfmFromMarkdown } from "mdast-util-gfm";
import { gfm } from "micromark-extension-gfm";
import remarkBreaks from "remark-breaks";
// Declares the processor data the GFM plugin below writes to.
import type {} from "remark-parse";
import type { Processor } from "unified";

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

// Chat Markdown: GitHub flavour, with single newlines kept as breaks.
export const CHAT_PLUGINS = [remarkGfm, remarkBreaks];
// A written document means its single newlines as soft wraps, the way every
// other renderer reads a README; only chat prose relies on them as breaks.
export const DOCUMENT_PLUGINS = [remarkGfm];
