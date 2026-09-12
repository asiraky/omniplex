import type { Preview } from "~/protocol";

/**
 * The running dev servers of the open session, as tappable chips above the
 * composer.
 *
 * This sits next to the conversation rather than in a details panel because
 * of when it is wanted: the agent brings a UI up mid-turn, and the next thing
 * you want is to look at it. A link two taps away inside a panel would be a
 * link you forget exists.
 *
 * Each chip navigates to the server rather than straight to the service. Only
 * the server knows which of a service's addresses this device can reach, and
 * a published preview needs a single-use ticket minted per open — neither is
 * something the client can work out.
 */
export function PreviewStrip({ previews }: { previews: Preview[] }) {
  if (previews.length === 0) return null;

  return (
    <div className="flex justify-center px-3 pb-1.5">
      {/* Scrolls rather than wraps: a project with five services must not
          push the composer down the screen on a phone. */}
      <div className="flex max-w-full gap-1.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {previews.map((preview) => (
          <a
            key={preview.id}
            href={`/api/previews/${encodeURIComponent(preview.id)}/open`}
            target="_blank"
            rel="noreferrer"
            title={`${previewName(preview)} — ${preview.url}`}
            className="bg-card/90 text-muted-foreground hover:text-foreground focus-visible:ring-ring flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1 text-[12px] shadow-sm backdrop-blur outline-none focus-visible:ring-2"
          >
            <span className="bg-primary/70 inline-flex size-2 shrink-0 rounded-full" aria-hidden />
            <span className="max-w-[10rem] truncate">{previewName(preview)}</span>
            <ExternalIcon />
          </a>
        ))}
      </div>
    </div>
  );
}

/**
 * What to call a service. A project that named it wins; anything we merely
 * noticed is described by its port, which is honest and is also what the
 * person who started it will recognise.
 */
export function previewName(preview: Preview): string {
  if (preview.source === "declared" && preview.label) return preview.label;
  if (preview.label && preview.label !== String(preview.port)) {
    return `${preview.label} · ${preview.port}`;
  }
  return `port ${preview.port}`;
}

function ExternalIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      className="size-3 shrink-0 opacity-60"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M15 3h6v6" />
      <path d="M10 14 21 3" />
      <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
    </svg>
  );
}
