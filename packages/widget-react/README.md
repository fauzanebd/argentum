# `@argentum/widget-react`

The Argentum chat widget as a React component.

```bash
npm install @argentum/widget-react
```

```tsx
import { ArgentumWidget } from "@argentum/widget-react";

export function Support({ identity, resign }) {
  return (
    <ArgentumWidget
      clientKey={identity.clientKey}
      apiBase={identity.apiBase}
      appBase="https://cdn.example.com/widget/v1/app/"
      user={identity.user}
      onTokenExpired={resign}
    />
  );
}
```

It renders `null`. The loader appends its own launcher and iframe to `<body>`,
so there is nothing for React to place, and an empty `<div>` would only be a
zero-height child your flex row has to style around.

## Props

Every [`init()` option](../widget/README.md#options) is a prop, plus three
callbacks: `onReady`, `onTokenExpired`, `onError`.

`onTokenExpired` is the one a working integration cannot skip. Signed tokens
last 15 minutes; re-sign on your server and the component calls `identify()`
with the new signature by itself.

## What it handles that a hand-rolled `useEffect` usually does not

- **An inline arrow for a callback does not remount the iframe.** Handlers are
  held in a ref, so a parent re-render does not drop the conversation a visitor
  is in the middle of.
- **A re-signed token does not remount it either.** A new `user.sig` arrives
  every fifteen minutes; it becomes an `identify()` call, not a teardown.
- **`destroy()` on unmount**, including in React 18 StrictMode's double-mount.

## It is a wrapper

Everything the widget *does* lives in `@argentum/widget`. This file owns when to
start, when to stop, and what to do when the visitor changes — nothing else. A
second implementation of the iframe bridge would be a second thing to keep in
step with the app inside the iframe.
