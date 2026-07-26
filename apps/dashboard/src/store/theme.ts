import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { Theme } from "@/lib/theme";

interface ThemeState {
  theme: Theme;
  toggle: () => void;
  setTheme: (theme: Theme) => void;
}

export const useThemeStore = create<ThemeState>()(
  persist(
    (set, get) => ({
      theme: "dark",
      toggle: () => set({ theme: get().theme === "light" ? "dark" : "light" }),
      setTheme: (theme) => set({ theme }),
    }),
    { name: "argentum-theme" }
  )
);
