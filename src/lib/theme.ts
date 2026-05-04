/**
 * Argentum Design System — Color Tokens
 *
 * These are the single source of truth for the color palette.
 * CSS variables in index.css reference these values.
 * Change here to update the whole theme.
 */

export const LIGHT_COLORS = {
  /** Large background areas (page, sidebar) */
  cream: "#F5F5F0",
  /** Cards, sections, inputs, dropdowns */
  white: "#FFFFFF",
  /** Accent / decorative — send button, active indicator, hover accents */
  red: "#F25C5C",
  /** Primary text */
  text: "#0A0A0A",
  /** Muted text (subtitles, timestamps) */
  textMuted: "#6B6B6B",
  /** Borders */
  border: "#E2E2DC",
} as const;

export const DARK_COLORS = {
  /** Page / app background */
  base: "#212427",
  /** Sidebar, card surfaces */
  surface: "#2A2D31",
  /** Hover states, elevated elements */
  elevated: "#313539",
  /** Input backgrounds */
  input: "#2A2D31",
  /** Silver shimmer color for the floating sidebar */
  silver: "#C0C0C0",
  /** Accent — same red for consistency */
  red: "#F25C5C",
  /** Primary text */
  text: "#F0F0EE",
  /** Muted text */
  textMuted: "#8A8F98",
  /** Borders */
  border: "#35393E",
} as const;

export type Theme = "light" | "dark";
