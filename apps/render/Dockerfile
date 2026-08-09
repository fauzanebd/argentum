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

ENV PNPM_HOME=/pnpm PATH=$PNPM_HOME:$PATH
RUN corepack enable

WORKDIR /app

# The workspace files first, so a dependency change is the only thing that
# invalidates the install layer.
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
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
RUN pnpm --filter @argentum/render exec npx remotion browser ensure

# Not root. The browser sandbox stays on; if a deployment cannot support it the
# answer is a seccomp profile and the NetworkPolicy, not --no-sandbox added
# quietly here.
RUN useradd --create-home --shell /bin/bash render \
    && chown -R render:render /app
USER render

ENV RENDER_PORT=8090
EXPOSE 8090
CMD ["pnpm", "--filter", "@argentum/render", "start"]
