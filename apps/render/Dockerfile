# The one image in this repository with a browser in it.
#
# That is deliberate and it is fenced: apps/backend's four images stay static
# binaries on alpine, and nothing about a PDF, an XLSX or a PPTX goes near this
# container. backlog.md's rejection of headless-Chromium *document* rendering is
# not reopened by it — see the T-V2 ticket.
FROM node:22-bookworm-slim

# Chromium's own dependencies, plus ffmpeg for the encode. Remotion downloads
# chrome-headless-shell itself; these are the shared libraries it needs to
# start, and a missing one is a container that boots and then fails its first
# render — which is why the readiness probe renders rather than pings.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg fonts-liberation libasound2 libatk-bridge2.0-0 \
      libatk1.0-0 libc6 libcairo2 libcups2 libdbus-1-3 libdrm2 libexpat1 \
      libgbm1 libglib2.0-0 libnspr4 libnss3 libpango-1.0-0 libx11-6 \
      libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxkbcommon0 \
      libxrandr2 xdg-utils \
    && rm -rf /var/lib/apt/lists/*

# Two instructions, not one: a variable set in an ENV is not expanded by the
# same ENV, so `PATH=$PNPM_HOME:$PATH` on one line prepends an empty string and
# the store's binaries are not on the path at all.
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
# Pinned, not whatever corepack resolves on the day of the build. There is no
# root package.json to carry a `packageManager` field, so an unpinned corepack
# takes the newest release — and the first build of this image met a pnpm whose
# `minimumReleaseAge` policy refuses any package published in the last 24 hours,
# which is a description of a lockfile somebody updated yesterday. An image
# whose package manager changes under it is an image that fails on a day nobody
# touched it.
RUN corepack enable && corepack prepare pnpm@10.13.1 --activate

WORKDIR /app

# The workspace files first, so a dependency change is the only thing that
# invalidates the install layer.
#
# **There is no root package.json in this repository** and this line used to
# copy one. pnpm reads pnpm-workspace.yaml for the member list and the lockfile
# for what to install, so the root manifest is not needed to install a member —
# it was named here because most workspaces have one, and the image was never
# built to find out.
COPY pnpm-workspace.yaml pnpm-lock.yaml ./
COPY packages/api-types/package.json packages/api-types/
COPY packages/motion/package.json packages/motion/
COPY apps/render/package.json apps/render/
RUN pnpm install --frozen-lockfile --filter @argentum/render...

COPY packages/api-types packages/api-types
COPY packages/motion packages/motion
COPY apps/render apps/render

# Warm the Chromium download at build time. Doing it on first request would put
# a 94 MB download inside a tenant's first report, and doing it never is a pod
# that needs egress it is not supposed to have.
#
# Through `@remotion/renderer`'s own `ensureBrowser`, not the `remotion` CLI:
# this service depends on the bundler and the renderer, never on `@remotion/cli`,
# so `npx remotion` — what this line used to say — answers "could not determine
# executable to run". The script lives in package.json so the image and a
# developer reach the browser the same way.
RUN pnpm --filter @argentum/render run browser:ensure

# Not root. The browser sandbox stays on; if a deployment cannot support it the
# answer is a seccomp profile and the NetworkPolicy, not --no-sandbox added
# quietly here.
RUN useradd --create-home --shell /bin/bash render \
    && chown -R render:render /app
USER render

ENV RENDER_PORT=8090
EXPOSE 8090

# **No package manager at runtime.** This used to be
# `pnpm --filter @argentum/render start`, and the first container started from
# this image never reached the server: corepack's cache belongs to root, the
# process runs as `render`, so pnpm was re-downloaded from the npm registry on
# boot — by a container this repository deploys behind a NetworkPolicy with
# `egress: []`. It would have failed in the cluster and worked on a laptop,
# which is the failure class T-02's zoneinfo finding named. pnpm 11 then wanted
# to purge and reinstall `node_modules` that the build had already written.
#
# `tsx` resolves from apps/render's own node_modules, so the entrypoint is the
# runtime and the file and nothing else.
WORKDIR /app/apps/render
CMD ["node", "--import", "tsx", "src/server.ts"]
