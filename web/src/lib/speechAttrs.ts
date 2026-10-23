// The markers the page leaves for read-aloud, apart from the converter in
// `~/lib/speech` so that marking a message does not load it.

/** Marks a message's content element; a selection reads only what is inside one. */
export const SPEAK_ROOT_ATTR = "data-speak-root";

/**
 * Says what a rendered element is when the DOM alone no longer does:
 * "code-block" and "diagram" for fenced blocks (whose header, copy button and
 * render status would otherwise be read), "code" for inline code drawn as
 * something other than `<code>`, like a clickable path chip.
 */
export const SPEAK_AS_ATTR = "data-speak-as";
