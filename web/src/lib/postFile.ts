/**
 * POSTs one file as the request body and resolves to the JSON reply.
 *
 * XHR rather than fetch because fetch reports nothing about an upload until it
 * is over, and a 40 MB PDF on a train is minutes of nothing without a progress
 * bar. The progress is also what tells a slow upload from a stalled one: see
 * withStallTimeout.
 *
 * A reply outside 2xx rejects with the server's `{error}` when it sent one; a
 * dropped connection rejects with a plain Error; an abort rejects with an
 * AbortError DOMException, which callers read as "taken back", not a failure.
 */
export function postFile<T>(
  url: string,
  file: File,
  opts: { signal?: AbortSignal; onProgress?: (fraction: number) => void } = {},
): Promise<T> {
  return new Promise((resolve, reject) => {
    if (opts.signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    xhr.responseType = "text";
    const onAbort = () => xhr.abort();
    opts.signal?.addEventListener("abort", onAbort, { once: true });
    const done = () => opts.signal?.removeEventListener("abort", onAbort);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) opts.onProgress?.(e.loaded / e.total);
    };
    xhr.onload = () => {
      done();
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        body = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300 && body) {
        resolve(body as T);
        return;
      }
      const message = (body as { error?: string } | undefined)?.error;
      reject(new Error(message || `upload failed (${xhr.status})`));
    };
    xhr.onerror = () => {
      done();
      reject(new Error("upload failed: the connection dropped"));
    };
    xhr.onabort = () => {
      done();
      reject(new DOMException("Aborted", "AbortError"));
    };
    xhr.send(file);
  });
}
