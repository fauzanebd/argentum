# Argentum landing — Vite + React SPA, served as static files by nginx.
#
# BUILD CONTEXT IS THE REPOSITORY ROOT, not apps/landing. The landing's build
# script (apps/landing/scripts/build-docs.mjs) publishes the client-facing API
# docs and reads docs/api/**, apps/backend/openapi/v1.yaml and
# apps/backend/docs/postman from the workspace root. Building from apps/landing
# alone would fail the docs step. See .github/workflows/build-landing.yaml.

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable

# Full workspace copy: pnpm resolves the monorepo lockfile and the landing's
# docs step needs the repo-root paths above. .dockerignore keeps node_modules,
# dist and .git out of the context.
COPY . .
RUN pnpm install --frozen-lockfile
RUN pnpm --filter landing build

FROM nginx:1.27-alpine
COPY --from=build /app/apps/landing/dist /usr/share/nginx/html

# gzip, cache headers, security headers and a real 404 — see the file itself.
# The landing has no client-side routes, so there is no index.html fallback.
COPY apps/landing/nginx/default.conf /etc/nginx/conf.d/default.conf
COPY apps/landing/nginx/security-headers.conf /etc/nginx/snippets/security-headers.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
