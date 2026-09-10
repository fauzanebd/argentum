# Changesets

Versions for the two published npm packages, `@argentum/widget` and
`@argentum/widget-react` (T-22).

**Why this exists at all.** The release workflow's tag versions the deployed
backend images. The widget's version is a promise to somebody else's build: an
integrator pins `^0.1.0` and expects a patch not to change the `init()` options.
Coupling the two would mean a backend deploy bumping a package nothing in it
touched, and an integrator's lockfile churning for a Go change.

Everything else in the workspace is in `ignore` — those are applications and
internal packages, and they version with the repo.

## Adding one

```bash
pnpm changeset          # pick the packages, pick the bump, write the line
git add .changeset
```

The line you write is the changelog entry an integrator reads. Say what changed
for *them* — "`identify()` no longer rebuilds the iframe" — not which file moved.

## Releasing

```bash
pnpm version-packages   # applies the changesets, writes CHANGELOG.md, bumps
pnpm release            # builds both packages, then changeset publish
```

`access` is `restricted`: these publish to a private scope. Making a package
public is a deliberate edit to its own `publishConfig`, not a default anybody
inherits by adding a package here.
