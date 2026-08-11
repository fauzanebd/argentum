import React, { useEffect } from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { MotionConfig } from "framer-motion";
import { routeTree } from "./routes";
import { useThemeStore } from "./store/theme";
import { Toaster } from "./components/ui/toaster";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
});

const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

function ThemeProvider({ children }: { children: React.ReactNode }) {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }, [theme]);

  return <>{children}</>;
}

// `reducedMotion="user"` is the third of the three layers described in
// src/lib/motion.ts: it turns off transform and layout animation library-wide
// for anyone whose system asks for less motion, including components that never
// import the hooks in that file. The CSS block in index.css covers everything
// framer does not touch, and the hooks cover the variants themselves.
ReactDOM.createRoot(document.getElementById("root")!).render(
  <ThemeProvider>
    <MotionConfig reducedMotion="user">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        <Toaster />
      </QueryClientProvider>
    </MotionConfig>
  </ThemeProvider>,
);
