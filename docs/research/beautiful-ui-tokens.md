# Beautiful UI — extracted design tokens

Source: https://beautiful-ui-five.vercel.app/ — read from live stylesheet rules
(`:root` block) and `getComputedStyle` on 2026-08-11. Not from the marketing copy.

Mechanism: `.dark` class on the root element — **the same mechanism Argentum
already uses** (`darkMode: ["class"]` in tailwind.config.ts).

## Light (`:root`)

| var | value | role |
|---|---|---|
| `--page` | `#fafafb` | page ground |
| `--canvas` | `#f1f2f3` | recessed ground |
| `--surface` | `#ffffff` | cards |
| `--inset` | `#f7f8f9` | inset panels |
| `--hover` | `#f4f5f6` | hover fill |
| `--hover-2` | `#e7e9eb` | stronger hover |
| `--ink` | `#1f2124` | body text |
| `--ink-2` | `#62656b` | secondary text |
| `--ink-3` | `#9a9da3` | tertiary / placeholder |
| `--line` | `#ecedef` | hairline |
| `--line-strong` | `#e0e2e5` | stronger rule |
| `--field` | `#f2f2f3` | input fill |
| `--stripe` | `#49494913` | table zebra |
| `--stripe-bg` | `#f5f5f5` | zebra ground |
| `--accent` | `#0285ff` | primary accent (blue) |
| `--accent-ink` | `#0170dd` | accent text |
| `--accent-tint` | `#e9f3ff` | accent wash |
| `--green` | `#189a4d` | positive |
| `--green-tint` | `#e8f5ed` | |
| `--orange` | `#ef720c` | warning |
| `--orange-tint` | `#fdf1e5` | |
| `--red` | `#e3474c` | destructive |
| `--red-tint` | `#fcecec` | |
| `--tooltip-bg` | `#25272b` | |
| `--tooltip-fg` | `#f6f7f8` | |

## Dark (`.dark`)

| var | value |
|---|---|
| `--page` | `#17181a` |
| `--canvas` | `#1c1d1f` |
| `--surface` | `#232427` |
| `--inset` | `#1f2022` |
| `--hover` | `#2a2b2e` |
| `--hover-2` | `#313236` |
| `--ink` | `#f2f3f4` |
| `--ink-2` | `#a5a8ad` |
| `--ink-3` | `#6c6f75` |
| `--line` | `#2e3033` |
| `--line-strong` | `#3a3c40` |
| `--field` | `#2b2c2f` |
| `--stripe` | `#ffffff0e` |
| `--stripe-bg` | `#1b1c1e` |
| `--accent` | `#3d9aff` |
| `--accent-ink` | `#7ec0ff` |
| `--accent-tint` | `#3d9aff29` |
| `--green` | `#3dbb72` |
| `--orange` | `#f68f3c` |
| `--red` | `#ee5c61` |
| `--tooltip-bg` | `#111214` |

Tints in dark are **alpha over the ground** (`24`/`29` hex ≈ 14–16%), not solid
mixes. Worth copying — it survives any surface it lands on.

## Shadows

```
--shadow-hairline:    0 0 0 1px var(--line)
--shadow-btn:         0 0 0 1px var(--line-strong), 0 1px 2px #0000004d
--shadow-card:        0 0 0 1px var(--line), 0 1px 2px #0003, 0 2px 6px #0003
--shadow-raised:      0 0 0 1px var(--line), 0 2px 10px #00000038
--shadow-overlay:     0 0 0 1px var(--line-strong), 0 8px 28px #00000057
--shadow-inset-field: inset 0 1px 2px #0006
```

Every elevation carries its own hairline in the same declaration — border and
shadow are one token, never two classes.

## Radius — measured, not declared

No `--radius` variable. Literals, by frequency: **6px (184 nodes)**, 8px (69),
7px (28), 14px (22), 10px (14), 4px (15), pill (`1.67e7px`, 81), 50% (84).

→ controls 6px, cards 8px, large panels 14px, chips pill.

## Type

`Inter`, base **14px/21px**. Measured distribution:
12px/18px w400 · 12.5px/18.75px w400+w500 · 14px/21px w400 · 13px/21px w400 ·
11.5px/17.25px · 10px/15px **w650** (labels) · 13px/19.5px w600.

Dense: meta text lives at 10–11.5px, body at 12.5–13px.

## Divergence from Argentum today

| | Argentum | Beautiful UI |
|---|---|---|
| ground | `#F5F5F0` warm cream | `#fafafb` neutral grey |
| accent | `#F25C5C` brand red | `#0285ff` blue |
| radius | `0.75rem` / 12px everywhere | 6px controls, 8px cards |
| font | Space Grotesk | Inter |
| base size | 14px | 12.5–13px |
| red | brand primary | destructive only (`#e3474c`) |
| elevation | `shadow-sm` | hairline+shadow composite tokens |

A summary read off the rendered page rather than the stylesheet got three of
these wrong — it reported teal/blue/orange accents, 16px card radii, and no
ground colours at all. Everything above is measured, which is why this file
exists instead of a paragraph in a ticket.

## What Argentum took, and what it did not (T-U1)

**Took:** the neutral ramp, rung for rung; the semantic green and orange; the
tint-plus-ink pairing; the composite elevation tokens; 8px/6px radii.

**Did not take:** the blue accent. `--accent: #0285FF` is the one value that
would have replaced Argentum's brand — `#F25C5C` stays primary in both modes, and
Beautiful UI's blue has no counterpart in the system. Its red `#E3474C` was taken
for `destructive` only, which is the role it plays there too.

**Added, with no counterpart in the reference:** the `*Ink` tokens. Beautiful UI
has exactly one (`--accent-ink`), because a blue accent only needs darkening for
one role. A red-primary system with green and orange semantics needs four, and
each is derived rather than picked — see `tokens.json` §`$ink`.

**Deliberately not adopted:** Inter. The family is embedded as TTF in the Go
report renderer, so the swap costs a backend change and a re-verification of
every PDF's metrics. Space Grotesk stays; the *scale* tightens in `T-U8`.
