import type { DeleteThreadConfirm } from "./DeleteThreadConfirm";

// The delete confirmation, fetched once something that can delete is on
// screen rather than with the first paint. Nobody deletes a thread in the
// first second, and the initial bundle is what a phone on 4G waits for; by the
// time an X is tapped it has long arrived.
let Confirm: typeof DeleteThreadConfirm | undefined;
let loading: Promise<void> | undefined;

/** The confirmation, if its code has arrived. */
export function loadedDeleteThreadDialog() {
  return Confirm;
}

export function loadDeleteThreadDialog(): Promise<void> {
  loading ??= import("./DeleteThreadConfirm").then(
    (m) => {
      Confirm = m.DeleteThreadConfirm;
    },
    () => {
      // Offline, most likely: the next ask tries again.
      loading = undefined;
    },
  );
  return loading;
}
