import type { toast as sonnerToast } from "sonner";

type Toast = typeof sonnerToast;

// Sonner is a toast library plus its stylesheet as a string, and nothing it
// does is needed until something goes wrong. So it loads after first paint
// rather than with the app: the Toaster is lazy in main.tsx, and a toast raised
// before either has arrived waits for the module. Sonner replays the toasts it
// already holds to a Toaster that subscribes late, so none are lost.
let sonner: Promise<Toast> | undefined;
const load = () =>
  (sonner ??= import("sonner").then(
    (m) => m.toast,
    (err: unknown) => {
      sonner = undefined; // a later toast tries the fetch again
      throw err;
    },
  ));

type Args = Parameters<Toast["message"]>;

const later =
  (kind: "success" | "info" | "error") =>
  (...args: Args) => {
    load().then((t) => t[kind](...args), () => {});
  };

export const toast = {
  success: later("success"),
  info: later("info"),
  error: later("error"),
};
