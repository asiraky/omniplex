import { CheckIcon, CopyIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { CodeLines } from "~/components/CodeLines";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Spinner } from "~/components/ui/spinner";
import { useCopy } from "~/lib/clipboard";
import { blobSize, blobTitle, type Blob } from "~/lib/composerRefs";
import { fileIconFor } from "~/lib/fileIcons";
import type { FileContent } from "~/protocol";

/** What a chip opens: a collapsed paste, an attached picture, or a referenced file. */
export type QuickViewTarget =
  | { kind: "blob"; blob: Blob }
  | { kind: "image"; src: string; name: string }
  | { kind: "file"; path: string; line?: number };

/**
 * The chip's full contents, one tap away.
 *
 * A chip has room for a label and a line of preview, which is enough to
 * recognise something but not to check it — and checking it is exactly what
 * you want before sending a 400-line log to an agent. Rather than three
 * viewers, everything a chip can hold opens here: the same dialog reads a
 * paste, a picture and a file, so the gesture is learned once.
 *
 * A file's bytes are fetched only when its quickview is actually opened. The
 * chip itself never carries them, which is what keeps a message with six file
 * references cheap on a phone.
 */
export function QuickView({
  target,
  onClose,
  loadFile,
}: {
  target: QuickViewTarget | null;
  onClose: () => void;
  /** Only needed for `file` targets; the panel's own reader, reused. */
  loadFile?: (path: string) => Promise<FileContent>;
}) {
  return (
    <Dialog open={!!target} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        fullscreenOnMobile
        className="flex max-h-[85dvh] flex-col gap-0 p-0 sm:max-w-3xl"
      >
        {target && <Body target={target} loadFile={loadFile} />}
      </DialogContent>
    </Dialog>
  );
}

function Body({
  target,
  loadFile,
}: {
  target: QuickViewTarget;
  loadFile?: (path: string) => Promise<FileContent>;
}) {
  switch (target.kind) {
    case "blob":
      return <BlobView blob={target.blob} />;
    case "image":
      return <ImageView src={target.src} name={target.name} />;
    case "file":
      return <FilePreview path={target.path} line={target.line} loadFile={loadFile} />;
  }
}

/** The heading every quickview shares: an icon, a full title, and a subtitle
    that says how much there is. The close button lives in the dialog itself,
    so the row leaves room for it. */
function Head({
  icon,
  title,
  subtitle,
  action,
}: {
  icon: React.ReactNode;
  title: string;
  subtitle?: string;
  action?: React.ReactNode;
}) {
  return (
    <DialogHeader className="flex-row items-start gap-2 space-y-0 border-b p-3 pr-14 text-left">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span className="min-w-0 flex-1">
        {/* Long paths break rather than truncate: the tail of a path is the
            part that identifies it, and it is what a middle ellipsis eats. */}
        <DialogTitle className="min-w-0 font-mono text-[12px] leading-snug break-all">
          {title}
        </DialogTitle>
        {subtitle && (
          <DialogDescription className="text-[11px]">{subtitle}</DialogDescription>
        )}
      </span>
      {action}
    </DialogHeader>
  );
}

function CopyButton({ text }: { text: string }) {
  const { copied, copy } = useCopy();
  return (
    <Button variant="ghost" size="sm" className="h-8 shrink-0 gap-1.5" onClick={() => void copy(text)}>
      {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
      <span className="max-sm:sr-only">Copy</span>
    </Button>
  );
}

function BlobView({ blob }: { blob: Blob }) {
  const icon =
    blob.origin.kind === "file" ? (
      (() => {
        const { Icon, tone } = fileIconFor(blob.origin.path);
        return <Icon className={`size-4 ${tone}`} />;
      })()
    ) : (
      <span className="text-muted-foreground text-[13px]">≡</span>
    );
  return (
    <>
      <Head
        icon={icon}
        title={blobTitle(blob)}
        subtitle={blobSize(blob)}
        action={<CopyButton text={blob.text} />}
      />
      <div className="scroll-thin min-h-0 flex-1 overflow-auto overscroll-contain py-2">
        <CodeLines
          content={blob.text}
          startLine={blob.origin.kind === "file" ? blob.origin.from : 1}
        />
      </div>
    </>
  );
}

function ImageView({ src, name }: { src: string; name: string }) {
  return (
    <>
      <Head icon={<span className="text-muted-foreground text-[13px]">▣</span>} title={name} />
      <div className="scroll-thin min-h-0 flex-1 overflow-auto overscroll-contain p-3">
        <img src={src} alt={name} className="mx-auto max-h-full w-auto max-w-full rounded-md" />
      </div>
    </>
  );
}

function FilePreview({
  path,
  line,
  loadFile,
}: {
  path: string;
  line?: number;
  loadFile?: (path: string) => Promise<FileContent>;
}) {
  const [file, setFile] = useState<FileContent | null>(null);
  const [error, setError] = useState("");
  const { Icon, tone } = fileIconFor(path);

  useEffect(() => {
    if (!loadFile) return;
    let stale = false;
    setFile(null);
    setError("");
    loadFile(path)
      .then((f) => !stale && setFile(f))
      .catch((e) => !stale && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      stale = true;
    };
    // The reader is re-created on every App render; re-running on it would
    // turn one read into a loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  return (
    <>
      <Head
        icon={<Icon className={`size-4 ${tone}`} />}
        title={path}
        subtitle={line ? `from line ${line}` : undefined}
        action={file && !file.binary ? <CopyButton text={file.content} /> : undefined}
      />
      <div className="scroll-thin min-h-0 flex-1 overflow-auto overscroll-contain py-2">
        {error && (
          <p className="text-destructive flex items-start gap-2 px-3 py-3 text-[12px]">
            <TriangleAlertIcon className="size-4 shrink-0" />
            <span className="min-w-0 break-words">{error}</span>
          </p>
        )}
        {!error && !file && (
          <p className="text-muted-foreground flex items-center gap-2 px-3 py-4 text-[12px]">
            <Spinner className="text-primary size-3.5" /> Reading {path}…
          </p>
        )}
        {file?.binary && (
          <p className="text-muted-foreground px-3 py-4 text-[12px]">
            Binary file — nothing to show as text.
          </p>
        )}
        {file && !file.binary && <CodeLines content={file.content} highlight={line} />}
        {file?.truncated && (
          <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
            Truncated — the file is larger than the viewer will show.
          </p>
        )}
      </div>
    </>
  );
}
