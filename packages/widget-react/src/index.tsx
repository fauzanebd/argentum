import { useEffect, useRef } from "react";
import Argentum, { type InitOptions } from "@argentum/widget";

/**
 * `<ArgentumWidget />` — the loader's `init`/`destroy`/`identify` as a
 * component (T-22).
 *
 * **A wrapper, not a reimplementation.** Every behaviour here belongs to
 * `@argentum/widget`; this file owns exactly the three things React changes
 * about using it: when to start, when to stop, and what to do when the props
 * describing the visitor change. A second implementation of the iframe bridge
 * would be a second thing to keep in step with the app inside the iframe, and
 * the first bug would land on whichever half a tenant did not read.
 */
export type ArgentumWidgetProps = InitOptions & {
  /** Called once the iframe has a session. */
  onReady?: () => void;
  /** Called when the signed `exp` has passed and the host must re-sign.
   *
   *  Handled here rather than left to the caller's `useEffect` because it is
   *  the one event a working integration cannot skip: without it the widget
   *  stops answering fifteen minutes in, which reads as "the widget broke"
   *  rather than "the token was short-lived on purpose". */
  onTokenExpired?: () => void;
  onError?: (detail?: unknown) => void;
};

export function ArgentumWidget({
  onReady,
  onTokenExpired,
  onError,
  ...options
}: ArgentumWidgetProps) {
  // Handlers live in a ref so a caller passing an inline arrow does not tear
  // the iframe down and rebuild it on every render — the common React mistake
  // this component exists to make impossible.
  const cbs = useRef({ onReady, onTokenExpired, onError });
  cbs.current = { onReady, onTokenExpired, onError };

  // The identity is deliberately not in the mount effect's dependencies: a
  // re-signed token arrives as a new `user.sig` every fifteen minutes, and
  // rebuilding the iframe for it would drop the conversation the visitor is
  // in the middle of. Mount once per deployment identity; re-`identify` for
  // the rest.
  const { clientKey, apiBase, appBase, launcher, position, locale } = options;
  const themeKey = JSON.stringify(options.theme ?? null);
  const opts = useRef(options);
  opts.current = options;

  useEffect(() => {
    Argentum.init(opts.current)
      .on("ready", () => cbs.current.onReady?.())
      .on("token_expired", () => cbs.current.onTokenExpired?.())
      .on("error", (d: unknown) => cbs.current.onError?.(d));
    return () => Argentum.destroy();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clientKey, apiBase, appBase, launcher, position, locale, themeKey]);

  const { ref, name, exp, sig } = options.user;
  useEffect(() => {
    Argentum.identify({ ref, name, exp, sig });
  }, [ref, name, exp, sig]);

  // The loader appends its own iframe and launcher to <body>; there is nothing
  // for React to place. Returning null rather than an empty <div> keeps the
  // host's layout — a flex row, a grid — free of a zero-height child it would
  // otherwise have to style around.
  return null;
}

export default ArgentumWidget;
export type { InitOptions, WidgetTheme } from "@argentum/widget";
