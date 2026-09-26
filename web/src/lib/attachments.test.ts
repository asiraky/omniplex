// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";

import { MAX_IMAGE_BYTES, sendPayload, stageFile, uploadStaged, type Attachment, type UploadDeps } from "~/lib/attachments";

const file = (name: string, type: string, size = 3) => {
  const f = new File([new Uint8Array(Math.min(size, 16))], name, { type });
  if (size > 16) Object.defineProperty(f, "size", { value: size });
  return f;
};

function deps(over: Partial<UploadDeps> = {}): UploadDeps {
  return {
    prepare: vi.fn(async (f: File) => f),
    uploadImage: vi.fn(async () => ({ id: "img-1", mediaType: "image/png", size: 3 })),
    uploadFile: vi.fn(async () => ({
      artefact: { id: "art-1", name: "x", versions: [] },
      version: 2,
    })),
    ...over,
  };
}

describe("uploadStaged", () => {
  it("sends a picture the model can take down the image path", async () => {
    const d = deps();
    const patch = await uploadStaged("s1", file("shot.png", "image/png"), {}, d);
    expect(patch).toEqual({ status: "ready", id: "img-1" });
    expect(d.uploadFile).not.toHaveBeenCalled();
  });

  it("sends anything else as an artefact, reporting progress", async () => {
    const onProgress = vi.fn();
    const d = deps({
      uploadFile: vi.fn(async (_s, _f, opts) => {
        opts.onProgress?.(0.5);
        return { artefact: { id: "art-9", name: "r.pdf", versions: [] }, version: 1 };
      }),
    });
    const patch = await uploadStaged("s1", file("r.pdf", "application/pdf"), { onProgress }, d);
    expect(patch).toMatchObject({ kind: "file", status: "ready", artefactId: "art-9", version: 1 });
    expect(onProgress).toHaveBeenCalledWith(0.5);
    expect(d.uploadImage).not.toHaveBeenCalled();
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
    } finally {
      URL.createObjectURL = original;
    }
  });
});

describe("sendPayload", () => {
  const a = (over: Partial<Attachment>): Attachment => ({ key: "k", name: "n", previewUrl: "", status: "ready", ...over });

  it("names ready images by id and ready files by artefact version, and drops the rest", () => {
    expect(
      sendPayload([
        a({ id: "img-1" }),
        a({ kind: "file", artefactId: "art-1", version: 3 }),
        a({ kind: "file", status: "uploading" }),
        a({ status: "error", error: "no" }),
        // An image that fell back to a file is sent as the file it became.
        a({ kind: "file", artefactId: "art-2", version: 1 }),
      ]),
    ).toEqual({
      imageIds: ["img-1"],
      files: [
        { artefactId: "art-1", version: 3 },
        { artefactId: "art-2", version: 1 },
      ],
    });
  });
});
