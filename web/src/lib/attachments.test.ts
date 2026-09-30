// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";

import { MAX_IMAGE_BYTES, MAX_PDF_BYTES, sendPayload, stageFile, uploadStaged, type Attachment, type UploadDeps } from "~/lib/attachments";
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
    expect(d.uploadImage).toHaveBeenCalledWith("s1", doc, undefined);

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
