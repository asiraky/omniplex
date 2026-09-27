/**
 * Images and PDFs attached to a prompt.
 *
 * A file is uploaded the moment it is picked, not when the message is
 * sent: on a phone on 4G a 3 MB screenshot takes seconds, and paying for that
 * after hitting send would make the composer feel broken. By the time there is
 * a message to send, all that goes over the socket is a list of ids.
 */

export const PDF_TYPE = "application/pdf";

/** What the server stores, and therefore what may be attached. */
export const ACCEPTED_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp", PDF_TYPE];

/** Matches internal/attachment.MaxBytes, which is itself the largest image the
    Claude API will take once base64 has inflated it by a third. Checked here so
    the phone finds out before it spends the upload rather than after. */
export const MAX_IMAGE_BYTES = 3_750_000;

/** Matches internal/attachment.MaxPDFBytes. A PDF is sent as it was picked:
    there is no shrinking one in a browser. */
export const MAX_PDF_BYTES = 10_000_000;

/** Matches internal/attachment.MaxPromptBytes: everything one message carries,
    together. Checked here because a refused send clears the composer. */
export const MAX_PROMPT_BYTES = 20_000_000;

/** The longest edge worth sending. Anthropic resizes anything larger than this
    before the model ever sees it, so uploading more is paying 4G for pixels
    that get thrown away. */
const MAX_EDGE = 1568;

/** Below this a picture is left exactly as it was picked: re-encoding a small
    screenshot only costs it sharpness. */
const REENCODE_OVER = 400 * 1024;

/** The `accept` attribute for a file input. Deliberately the same list the
    server enforces: offering a HEIC that will be refused is worse than not
    offering it. */
export const ATTACH_ACCEPT = ACCEPTED_TYPES.join(",");

export interface UploadedImage {
  id: string;
  mediaType: string;
  size: number;
}

/** One file in the composer, from picked to sendable. */
export interface Attachment {
  /** Local identity, stable across the upload. Not the server's id. */
  key: string;
  name: string;
  /** The picked file's type, which decides whether it gets a thumbnail. */
  mediaType: string;
  /** Object URL for an image's thumbnail, shown before the upload finishes.
      Empty for a PDF, which is shown as its name. */
  previewUrl: string;
  status: "uploading" | "ready" | "error";
  /** The server's id, present once uploaded. This is what the prompt names. */
  id?: string;
  /** Bytes the server stored, present once uploaded. */
  size?: number;
  error?: string;
}

export function isSupportedFile(file: File): boolean {
  return ACCEPTED_TYPES.includes(file.type);
}

/** Whether the uploaded files together are more than one message may carry. */
export function overPromptLimit(attachments: Attachment[]): boolean {
  const total = attachments.reduce((sum, a) => sum + (a.status === "ready" ? (a.size ?? 0) : 0), 0);
  return total > MAX_PROMPT_BYTES;
}

export function isPdf(mediaType: string): boolean {
  return mediaType === PDF_TYPE;
}

/** The largest file of this type the server will take. */
export function maxBytesFor(mediaType: string): number {
  return isPdf(mediaType) ? MAX_PDF_BYTES : MAX_IMAGE_BYTES;
}

/** Where a stored file is read back from. The device cookie rides the
    request, so this works straight from an `<img src>` or a link. */
export function attachmentUrl(sessionId: string, id: string): string {
  return `/api/sessions/${encodeURIComponent(sessionId)}/attachments/${encodeURIComponent(id)}`;
}

/** Uploads one file and returns how the prompt will refer to it. */
export async function uploadAttachment(
  sessionId: string,
  file: File,
  signal?: AbortSignal,
): Promise<UploadedImage> {
  const res = await fetch(
    `/api/sessions/${encodeURIComponent(sessionId)}/attachments`,
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
 * The images and PDFs in a drop or a paste.
 *
 * A screenshot pasted from the clipboard arrives as a file with no useful
 * name, and a drag from a browser carries the picture alongside its URL as
 * text — so this reads files only, and leaves anything else to the textarea.
 */
export function attachableFilesFrom(data: DataTransfer | null): File[] {
  if (!data) return [];
  return Array.from(data.files).filter((f) => f.type.startsWith("image/") || isPdf(f.type));
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
