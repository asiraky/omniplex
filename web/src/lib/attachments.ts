/**
 * Files attached to a prompt.
 *
 * A file is uploaded the moment it is picked, not when the message is
 * sent: on a phone on 4G a 3 MB screenshot takes seconds, and paying for that
 * after hitting send would make the composer feel broken. By the time there is
 * a message to send, all that goes over the socket is a list of ids.
 */

import { MAX_ARTEFACT_BYTES, uploadArtefact, type ArtefactRef, type UploadedArtefact } from "~/lib/artefacts";

export const PDF_TYPE = "application/pdf";

/** What the server stores, and therefore what may be attached as an image. */
export const ACCEPTED_IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

/** Matches internal/attachment.MaxBytes, which is itself the largest image the
    Claude API will take once base64 has inflated it by a third. Checked here so
    the phone finds out before it spends the upload rather than after. */
export const MAX_IMAGE_BYTES = 3_750_000;

/** The longest edge worth sending. Anthropic resizes anything larger than this
    before the model ever sees it, so uploading more is paying 4G for pixels
    that get thrown away. */
const MAX_EDGE = 1568;

/** Matches internal/attachment.MaxPDFBytes. A PDF is sent as it was picked:
    there is no shrinking one in a browser. */
export const MAX_PDF_BYTES = 10_000_000;

/** Matches internal/attachment.MaxPromptBytes: every image and PDF one message
    carries, together. Checked here because a refused send clears the composer. */
export const MAX_PROMPT_BYTES = 20_000_000;

/** Below this a picture is left exactly as it was picked: re-encoding a small
    screenshot only costs it sharpness. */
const REENCODE_OVER = 400 * 1024;

export interface UploadedImage {
  id: string;
  mediaType: string;
  size: number;
}

/**
 * One file in the composer, from picked to sendable.
 *
 * Two routes share the same staging. What the model can read directly, an
 * image or a PDF, goes to the attachment store and is named by `id`. Anything
 * else, a zip or a HEIC the store would refuse, goes up as an artefact and is
 * named by `artefactId`: the server saved it in the project's uploads folder
 * and tells the agent where. `kind` is only how the composer shows it.
 */
export interface Attachment {
  /** Local identity, stable across the upload. Not the server's id. */
  key: string;
  name: string;
  /** Absent means "image", which is what every attachment was before files. */
  kind?: "image" | "file";
  /** Object URL for an image's thumbnail, shown before the upload finishes.
      Empty for a file, which shows a tile instead. */
  previewUrl: string;
  /** "staged" is held in the browser, for a thread that does not exist yet:
      it goes up once the thread does. */
  status: "staged" | "uploading" | "ready" | "error";
  /** The attachment store's id, present once an image or PDF is uploaded. */
  id?: string;
  /** The artefact a file became, present once uploaded. */
  artefactId?: string;
  mediaType?: string;
  size?: number;
  /** 0..1 while a file uploads; images are small enough to go without. */
  progress?: number;
  error?: string;
}

export function isSupportedImage(file: File): boolean {
  return ACCEPTED_IMAGE_TYPES.includes(file.type);
}

export function isPdf(mediaType: string | undefined): boolean {
  return mediaType === PDF_TYPE;
}

/** Whether the images and PDFs uploaded to the store are, together, more than
    one message may carry. Files sent as artefacts are only a path to the
    agent, so they do not count. */
export function overPromptLimit(attachments: Attachment[]): boolean {
  let total = 0;
  for (const a of attachments) {
    if (a.status === "ready" && a.id && !a.artefactId) total += a.size ?? 0;
  }
  return total > MAX_PROMPT_BYTES;
}

/** Where a stored file is read back from. The device cookie rides the
    request, so this works straight from an `<img src>` or a link. */
export function attachmentUrl(threadId: string, id: string): string {
  return `/api/threads/${encodeURIComponent(threadId)}/attachments/${encodeURIComponent(id)}`;
}

/** Uploads one image or PDF and returns how the prompt will refer to it. */
export async function uploadAttachment(
  threadId: string,
  file: File,
  signal?: AbortSignal,
): Promise<UploadedImage> {
  const res = await fetch(
    `/api/threads/${encodeURIComponent(threadId)}/attachments`,
    {
      method: "POST",
      headers: { "Content-Type": file.type || "application/octet-stream" },
      body: file,
      // Removing a half-uploaded picture stops the upload: on a slow link the
      // rest of it is bandwidth spent on something already taken back.
      signal,
    },
  );
  if (!res.ok) {
    const message = await res
      .json()
      .then((b: { error?: string }) => b.error)
      .catch(() => "");
    throw new Error(message || `upload failed (${res.status})`);
  }
  return (await res.json()) as UploadedImage;
}

/**
 * The files in a drop or a paste.
 *
 * A screenshot pasted from the clipboard arrives as a file with no useful
 * name, and a drag from a browser carries the picture alongside its URL as
 * text — so this reads files only, and leaves anything else to the textarea.
 */
export function filesFrom(data: DataTransfer | null): File[] {
  if (!data) return [];
  return Array.from(data.files);
}

/** Whether a drag is carrying files at all, which decides if the composer
    should light up as a drop target. */
export function dragHasFiles(data: DataTransfer | null): boolean {
  return Array.from(data?.types ?? []).includes("Files");
}

/**
 * Shrinks a picture to something worth sending.
 *
 * A phone camera produces several megabytes of image whose long edge is four
 * times what the model will look at, and the upload is the slow half of
 * attaching it. Anything oversized or heavy is redrawn at MAX_EDGE and
 * re-encoded as JPEG; anything already small, or a GIF (whose animation a
 * canvas would flatten), is handed back untouched.
 *
 * Best effort by design: a browser that cannot decode the file, or a canvas
 * that refuses, gives the original back and lets the server have the last word.
 */
export async function prepareImage(file: File): Promise<File> {
  if (file.type === "image/gif") return file;
  try {
    const bitmap = await createImageBitmap(file);
    const longest = Math.max(bitmap.width, bitmap.height);
    const scale = Math.min(1, MAX_EDGE / longest);
    if (scale === 1 && file.size <= REENCODE_OVER) {
      bitmap.close();
      return file;
    }
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(bitmap.width * scale);
    canvas.height = Math.round(bitmap.height * scale);
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    bitmap.close();
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, "image/jpeg", 0.85),
    );
    // A picture that got bigger is not an improvement, and a flat colour PNG
    // often does exactly that.
    if (!blob || blob.size >= file.size) return file;
    const name = file.name.replace(/\.[^.]+$/, "") || "image";
    return new File([blob], `${name}.jpg`, { type: "image/jpeg" });
  } catch {
    return file;
  }
}

// ---- staging any file ----

/** Whether a file takes the image path. Anything else — including an image
    type the model cannot take — is uploaded as an artefact. */
export function isImageAttachment(file: File): boolean {
  return isSupportedImage(file);
}

/** The composer's entry for a file the moment it is picked, before any byte
    has gone up. `key` is the caller's: it must be unique, and
    `crypto.randomUUID` is not available on a plain-http origin. */
export function stageFile(file: File, key: string): Attachment {
  const image = isImageAttachment(file);
  return {
    key,
    kind: image ? "image" : "file",
    name: file.name || (image ? "pasted image" : "file"),
    // react-doctor-disable-next-line react-doctor/no-create-object-url-without-revoke -- the URL lives on the staged attachment; useComposerDrafts revokes it when the attachment is removed, sent, or its thread goes
    previewUrl: image ? URL.createObjectURL(file) : "",
    status: "uploading",
    mediaType: file.type || "application/octet-stream",
    size: file.size,
    // Only an artefact upload reports progress; see FileChip.
    ...(image || (isPdf(file.type) && file.size <= MAX_PDF_BYTES) ? {} : { progress: 0 }),
  };
}

export interface UploadDeps {
  prepare: (file: File) => Promise<File>;
  uploadImage: (threadId: string, file: File, signal?: AbortSignal) => Promise<UploadedImage>;
  uploadFile: (
    threadId: string,
    file: File,
    opts: { signal?: AbortSignal; onProgress?: (fraction: number) => void },
  ) => Promise<UploadedArtefact>;
}

const defaultDeps: UploadDeps = {
  prepare: prepareImage,
  uploadImage: uploadAttachment,
  uploadFile: uploadArtefact,
};

/**
 * Uploads a staged file and resolves to the patch that makes it sendable.
 *
 * A PDF goes to the store as it is, where Claude reads it natively. An image
 * that is still too heavy once shrunk, or a PDF over its limit, is not refused:
 * it goes up as a file instead, so the agent can still open it, and the patch
 * says so by turning the attachment's kind to "file". A file over the artefact
 * cap is refused here, before the upload spends the connection on it.
 */
export async function uploadStaged(
  threadId: string,
  file: File,
  opts: { signal?: AbortSignal; onProgress?: (fraction: number) => void } = {},
  deps: UploadDeps = defaultDeps,
): Promise<Partial<Attachment>> {
  if (isImageAttachment(file)) {
    const ready = await deps.prepare(file);
    if (ready.size <= MAX_IMAGE_BYTES) {
      const up = await deps.uploadImage(threadId, ready, opts.signal);
      return { status: "ready", id: up.id, size: up.size };
    }
  } else if (isPdf(file.type) && file.size <= MAX_PDF_BYTES) {
    const up = await deps.uploadImage(threadId, file, opts.signal);
    return { status: "ready", id: up.id, size: up.size, progress: 1 };
  }
  if (file.size > MAX_ARTEFACT_BYTES) throw new Error("This file is too large to send (200 MB at most).");
  const up = await deps.uploadFile(threadId, file, opts);
  return {
    kind: "file",
    status: "ready",
    artefactId: up.artefact.id,
    progress: 1,
  };
}

/** What a message carries: the ready images and PDFs by id and the ready
    files by artefact id. Anything still uploading or failed is left out. */
export function sendPayload(attachments: Attachment[]): { imageIds: string[]; files: ArtefactRef[] } {
  const imageIds: string[] = [];
  const files: ArtefactRef[] = [];
  for (const a of attachments) {
    if (a.status !== "ready") continue;
    if (a.artefactId) files.push({ artefactId: a.artefactId });
    else if (a.id) imageIds.push(a.id);
  }
  return { imageIds, files };
}
