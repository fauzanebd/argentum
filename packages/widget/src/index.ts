// The public surface of `@argentum/widget` (T-22).
//
// Deliberately narrower than the two files behind it. `protocol.ts` holds the
// whole postMessage vocabulary the loader and the iframe app agree on — an
// internal contract between two halves that always ship together, and one this
// package would be promising not to change if it exported it. What an
// integrator needs is the loader's API and the shapes they have to construct:
// the options object, the theme inside it, and the event names.

export { default } from "./loader";
export { default as Argentum } from "./loader";
export type { EventName, Handler, InitOptions, WidgetTheme } from "./loader";

// MARKER is exported because a host page that runs its own `message` listener
// needs to tell our frames apart from everybody else's; `isWidgetMessage` is
// the check that does it correctly, including the origin comparison a
// hand-written listener usually forgets.
export { MARKER, isWidgetMessage } from "./protocol";
export type { WidgetMessage } from "./protocol";
