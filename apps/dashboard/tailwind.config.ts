import type { Config } from "tailwindcss";
import tailwindcssAnimate from "tailwindcss-animate";
import typography from "@tailwindcss/typography";

export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
  	container: {
  		center: true,
  		padding: '1.5rem',
  		screens: {
  			'2xl': '1280px'
  		}
  	},
  	extend: {
  		colors: {
  			border: 'hsl(var(--border))',
  			input: 'hsl(var(--input))',
  			ring: 'hsl(var(--ring))',
  			background: 'hsl(var(--background))',
  			foreground: 'hsl(var(--foreground))',
  			primary: {
  				DEFAULT: 'hsl(var(--primary))',
  				foreground: 'hsl(var(--primary-foreground))',
  				tint: 'hsl(var(--primary-tint))',
  				ink: 'hsl(var(--primary-ink))'
  			},
  			secondary: {
  				DEFAULT: 'hsl(var(--secondary))',
  				foreground: 'hsl(var(--secondary-foreground))'
  			},
  			muted: {
  				DEFAULT: 'hsl(var(--muted))',
  				foreground: 'hsl(var(--muted-foreground))',
  				subtle: 'hsl(var(--muted-subtle))'
  			},
  			inset: 'hsl(var(--inset))',
  			field: 'hsl(var(--field))',
  			accent: {
  				DEFAULT: 'hsl(var(--accent))',
  				foreground: 'hsl(var(--accent-foreground))'
  			},
  			// The status family. `DEFAULT` is a fill, `ink` is the same meaning as
  			// text, `tint` is the ground the ink sits on. See tokens.json §$ink for
  			// why a fill colour and a text colour cannot be the same value.
  			destructive: {
  				DEFAULT: 'hsl(var(--destructive))',
  				foreground: 'hsl(var(--destructive-foreground))',
  				tint: 'hsl(var(--destructive-tint))',
  				ink: 'hsl(var(--destructive-ink))'
  			},
  			positive: {
  				DEFAULT: 'hsl(var(--positive))',
  				foreground: 'hsl(var(--positive-foreground))',
  				tint: 'hsl(var(--positive-tint))',
  				ink: 'hsl(var(--positive-ink))'
  			},
  			warning: {
  				DEFAULT: 'hsl(var(--warning))',
  				foreground: 'hsl(var(--warning-foreground))',
  				tint: 'hsl(var(--warning-tint))',
  				ink: 'hsl(var(--warning-ink))'
  			},
  			card: {
  				DEFAULT: 'hsl(var(--card))',
  				foreground: 'hsl(var(--card-foreground))'
  			},
  			popover: {
  				DEFAULT: 'hsl(var(--popover))',
  				foreground: 'hsl(var(--popover-foreground))'
  			},
  			sidebar: {
  				DEFAULT: 'hsl(var(--sidebar-background))',
  				foreground: 'hsl(var(--sidebar-foreground))',
  				primary: 'hsl(var(--sidebar-primary))',
  				'primary-foreground': 'hsl(var(--sidebar-primary-foreground))',
  				accent: 'hsl(var(--sidebar-accent))',
  				'accent-foreground': 'hsl(var(--sidebar-accent-foreground))',
  				border: 'hsl(var(--sidebar-border))',
  				ring: 'hsl(var(--sidebar-ring))'
  			}
  		},
  		borderRadius: {
  			lg: 'var(--radius)',
  			md: 'calc(var(--radius) - 2px)',
  			sm: 'calc(var(--radius) - 4px)'
  		},
  		// Elevation carries its own hairline. Every level below states the border
  		// and the shadow in one declaration, so a raised element cannot be given
  		// a shadow and then be missing its edge — the pairing is the token, not
  		// two classes a component has to remember to use together.
  		//
  		// `--border`/`--border-strong` are HSL triples, so the ring is written as
  		// hsl(var(--x)) here rather than as a colour utility.
  		boxShadow: {
  			hairline: '0 0 0 1px hsl(var(--border))',
  			control: '0 0 0 1px hsl(var(--border-strong)), 0 1px 2px rgb(0 0 0 / 0.06)',
  			card: '0 0 0 1px hsl(var(--border)), 0 1px 2px rgb(0 0 0 / 0.04), 0 2px 6px rgb(0 0 0 / 0.04)',
  			raised: '0 0 0 1px hsl(var(--border)), 0 2px 10px rgb(0 0 0 / 0.06)',
  			overlay: '0 0 0 1px hsl(var(--border-strong)), 0 8px 28px rgb(0 0 0 / 0.12)',
  			field: 'inset 0 1px 2px rgb(0 0 0 / 0.04)'
  		},
  		keyframes: {
  			'accordion-down': {
  				from: {
  					height: '0'
  				},
  				to: {
  					height: 'var(--radix-accordion-content-height)'
  				}
  			},
  			'accordion-up': {
  				from: {
  					height: 'var(--radix-accordion-content-height)'
  				},
  				to: {
  					height: '0'
  				}
  			},
  			// A sweep across a loading surface. Continuous and stateless, which is
  			// why it is a CSS keyframe rather than a framer variant — see
  			// src/lib/motion.ts `useShimmer`, which owns the decision to run it.
  			shimmer: {
  				from: {
  					backgroundPosition: '200% 0'
  				},
  				to: {
  					backgroundPosition: '-200% 0'
  				}
  			}
  		},
  		animation: {
  			'accordion-down': 'accordion-down 0.2s ease-out',
  			'accordion-up': 'accordion-up 0.2s ease-out',
  			// 1.6s is four entrances end to end. Slower than it looks like it
  			// should be on purpose: a fast shimmer reads as an error state.
  			shimmer: 'shimmer 1.6s linear infinite'
  		},
  		backgroundImage: {
  			// The gradient `animate-shimmer` sweeps. Tokenised rather than a literal
  			// so it inherits the surface ramp in both modes.
  			//
  			// Its background-size is NOT configured alongside it: Tailwind puts
  			// `backgroundImage` and `backgroundSize` in the same `bg-*` namespace, so
  			// a `backgroundSize.shimmer` key would emit a second `.bg-shimmer` rule
  			// and one of the two would silently lose. The size is an arbitrary value
  			// at the call site instead — see components/ui/shimmer.tsx.
  			shimmer:
  				'linear-gradient(90deg, transparent 0%, hsl(var(--muted)) 20%, hsl(var(--secondary)) 50%, hsl(var(--muted)) 80%, transparent 100%)'
  		}
  	}
  },
  plugins: [tailwindcssAnimate, typography],
} satisfies Config;
