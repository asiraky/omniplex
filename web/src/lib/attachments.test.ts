// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  MAX_IMAGE_BYTES,
  MAX_PDF_BYTES,
  UPLOAD_STALLED,
  sendPayload,
  stageFile,
  uploadStaged,
  withStallTimeout,
  type Attachment,
  type UploadDeps,
} from "~/lib/attachments";
import { makeArtefact } from "~/test/artefact";

const file = (name: string, type: string, size = 3) => {
  const f = new File([new Uint8Array(Math.min(size, 16))], name, { type });
  if (size > 16) Object.defineProperty(f, "size", { value: size });
  return f;
};

function deps(over: Partial<UploadDeps> = {}): UploadDeps {
  return {
    prepare: vi.fn(async (f: File) => f),
    uploadImage: vi.fn(async () => ({ id: "img-1", mediaType: "image/png", size: 3 })),
    uploadFile: vi.fn(async () => ({ artefact: makeArtefact({ id: "art-1", source: "upload" }) })),
    ...over,
  };
}

describe("uploadStaged", () => {
  it("sends a picture the model can take down the image path", async () => {
    const d = deps();
    const patch = await uploadStaged("s1", file("shot.png", "image/png"), {}, d);
    expect(patch).toEqual({ status: "ready", id: "img-1", size: 3 });
    expect(d.uploadFile).not.toHaveBeenCalled();
  });

  it("sends anything else as an artefact, reporting progress", async () => {
    const onProgress = vi.fn();
    const d = deps({
      uploadFile: vi.fn(async (_s, _f, opts) => {
        opts.onProgress?.(0.5);
        return { artefact: makeArtefact({ id: "art-9", name: "r.pdf", source: "upload" }) };
      }),
    });
    const patch = await uploadStaged("s1", file("r.zip", "application/zip"), { onProgress }, d);
    expect(patch).toMatchObject({ kind: "file", status: "ready", artefactId: "art-9" });
    expect(onProgress).toHaveBeenCalledWith(0.5);
    expect(d.uploadImage).not.toHaveBeenCalled();
  });

  it("sends a PDF to the attachment store untouched, and a too-big one as a file", async () => {
    const d = deps({ uploadImage: vi.fn(async () => ({ id: "doc-1", mediaType: "application/pdf", size: 9 })) });
    const doc = file("spec.pdf", "application/pdf", 9);
    expect(await uploadStaged("s1", doc, {}, d)).toMatchObject({ status: "ready", id: "doc-1", size: 9 });
    expect(d.prepare).not.toHaveBeenCalled();
    expect(d.uploadImage).toHaveBeenCalledWith("s1", doc, expect.objectContaining({ signal: expect.any(AbortSignal) }));

    const big = file("scan.pdf", "application/pdf", MAX_PDF_BYTES + 1);
    expect(await uploadStaged("s1", big, {}, d)).toMatchObject({ kind: "file", artefactId: "art-1" });
    expect(d.uploadFile).toHaveBeenCalledWith("s1", big, expect.anything());
  });

  it("sends an image type the model cannot take as a file", async () => {
    const d = deps();
    await uploadStaged("s1", file("IMG_1.heic", "image/heic"), {}, d);
    expect(d.prepare).not.toHaveBeenCalled();
    expect(d.uploadFile).toHaveBeenCalled();
  });

  it("falls back to a file when a picture is still too heavy once shrunk", async () => {
    const huge = file("scan.png", "image/png", MAX_IMAGE_BYTES + 1);
    const d = deps();
    const patch = await uploadStaged("s1", huge, {}, d);
    expect(d.uploadImage).not.toHaveBeenCalled();
    expect(d.uploadFile).toHaveBeenCalledWith("s1", huge, expect.anything());
    expect(patch.kind).toBe("file");
  });

  it("refuses a file over the cap before spending the upload", async () => {
    const d = deps();
    await expect(uploadStaged("s1", file("big.zip", "application/zip", 201 * 1024 * 1024), {}, d)).rejects.toThrow(
      /too large/,
    );
    expect(d.uploadFile).not.toHaveBeenCalled();
  });
});

describe("withStallTimeout", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  /** A run that never finishes on its own and rejects with an AbortError once
      its signal fires, as an XHR upload does. Hands back what it was given. */
  function hangingRun() {
    const got: { signal?: AbortSignal; onProgress?: (f: number) => void } = {};
    const run = (o: { signal: AbortSignal; onProgress: (f: number) => void }) => {
      Object.assign(got, o);
      return new Promise<string>((_resolve, reject) => {
        o.signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
      });
    };
    return { run, got };
  }

  it("fails an upload that sends nothing for stallMs, and aborts it", async () => {
    const { run, got } = hangingRun();
    const p = withStallTimeout(run, { stallMs: 1000 });
    const settled = vi.fn();
    p.catch(settled);
    await vi.advanceTimersByTimeAsync(999);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toBe(UPLOAD_STALLED);
    expect((err as Error).name).toBe("UploadStalled");
    expect(got.signal?.aborted).toBe(true);
  });

  it("keeps an upload that is still moving alive past stallMs in total", async () => {
    const onProgress = vi.fn();
    let finish!: (v: string) => void;
    let signal!: AbortSignal;
    let report!: (f: number) => void;
    const p = withStallTimeout(
      (o) => {
        signal = o.signal;
        report = o.onProgress;
        return new Promise<string>((resolve) => (finish = resolve));
      },
      { stallMs: 1000, onProgress },
    );
    for (let i = 1; i <= 5; i++) {
      await vi.advanceTimersByTimeAsync(900);
      report(i / 5);
    }
    finish("done");
    await expect(p).resolves.toBe("done");
    expect(onProgress).toHaveBeenLastCalledWith(1);
    expect(onProgress).toHaveBeenCalledTimes(5);
    expect(signal.aborted).toBe(false);
  });

  it("rejects with an AbortError, not a stall, when the caller takes the file back", async () => {
    const { run, got } = hangingRun();
    const outer = new AbortController();
    const p = withStallTimeout(run, { signal: outer.signal, stallMs: 1000 });
    outer.abort();
    const err = await p.catch((e: unknown) => e);
    expect((err as Error).name).toBe("AbortError");
    expect(got.signal?.aborted).toBe(true);
    // The watchdog is gone too, so nothing fires later.
    expect(vi.getTimerCount()).toBe(0);
  });

  it("rejects at once without running when the caller has already aborted", async () => {
    const outer = new AbortController();
    outer.abort();
    const run = vi.fn(async () => "never");
    const err = await withStallTimeout(run, { signal: outer.signal }).catch((e: unknown) => e);
    expect((err as Error).name).toBe("AbortError");
    expect(run).not.toHaveBeenCalled();
  });

  it("disarms the watchdog once the upload succeeds", async () => {
    let signal!: AbortSignal;
    const outer = new AbortController();
    const p = withStallTimeout(
      async (o) => {
        signal = o.signal;
        return "ok";
      },
      { signal: outer.signal, stallMs: 1000 },
    );
    await expect(p).resolves.toBe("ok");
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(5000);
    // Neither a late stall nor a late caller abort reaches the finished upload.
    outer.abort();
    expect(signal.aborted).toBe(false);
  });

  it("passes on what the upload rejects with when it fails by itself", async () => {
    const p = withStallTimeout(async () => {
      throw new Error("upload failed (500)");
    });
    await expect(p).rejects.toThrow("upload failed (500)");
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("uploadStaged under the watchdog", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("fails a stalled image upload, aborting the request", async () => {
    let signal: AbortSignal | undefined;
    const d = deps({
      uploadImage: vi.fn((_t: string, _f: File, o: { signal?: AbortSignal }) => {
        signal = o.signal;
        return new Promise<never>((_resolve, reject) => {
          o.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
        });
      }),
    });
    const p = uploadStaged("s1", file("shot.png", "image/png"), { stallMs: 1000 }, d);
    const caught = p.catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(1000);
    const err = (await caught) as Error;
    expect(err.name).toBe("UploadStalled");
    expect(err.message).toBe(UPLOAD_STALLED);
    expect(signal?.aborted).toBe(true);
  });

  it("forwards a PDF's progress to the caller", async () => {
    const onProgress = vi.fn();
    const d = deps({
      uploadImage: vi.fn(async (_t: string, _f: File, o: { onProgress?: (f: number) => void }) => {
        o.onProgress?.(0.3);
        return { id: "doc-1", mediaType: "application/pdf", size: 9 };
      }),
    });
    await uploadStaged("s1", file("spec.pdf", "application/pdf", 9), { onProgress }, d);
    expect(onProgress).toHaveBeenCalledWith(0.3);
  });
});

describe("stageFile", () => {
  it("gives images a thumbnail and files a progress bar", () => {
    const original = URL.createObjectURL;
    const createObjectURL = vi.fn(() => "blob:thumb");
    URL.createObjectURL = createObjectURL;
    try {
      const img = stageFile(file("a.png", "image/png"), "k1");
      const doc = stageFile(file("b.docx", ""), "k2");
      expect(img).toMatchObject({ kind: "image", previewUrl: "blob:thumb", status: "uploading" });
      expect(doc).toMatchObject({ kind: "file", previewUrl: "", progress: 0, mediaType: "application/octet-stream" });
      expect(createObjectURL).toHaveBeenCalledTimes(1);
      // A PDF under its limit goes up in one request with nothing to count.
      const pdf = stageFile(file("c.pdf", "application/pdf"), "k3");
      expect(pdf).toMatchObject({ kind: "file", previewUrl: "" });
      expect(pdf.progress).toBeUndefined();
    } finally {
      URL.createObjectURL = original;
    }
  });
});

describe("sendPayload", () => {
  const a = (over: Partial<Attachment>): Attachment => ({ key: "k", name: "n", previewUrl: "", status: "ready", ...over });

  it("names ready images by id and ready files by artefact id, and drops the rest", () => {
    expect(
      sendPayload([
        a({ id: "img-1" }),
        a({ kind: "file", artefactId: "art-1" }),
        a({ kind: "file", status: "uploading" }),
        a({ status: "error", error: "no" }),
        // An image that fell back to a file is sent as the file it became.
        a({ kind: "file", artefactId: "art-2" }),
      ]),
    ).toEqual({
      imageIds: ["img-1"],
      files: [{ artefactId: "art-1" }, { artefactId: "art-2" }],
    });
  });
});
