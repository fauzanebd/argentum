import {
  createRootRoute,
  createRoute,
  redirect,
  Outlet,
} from "@tanstack/react-router";
import { useAuthStore } from "@/store/auth";
import { AppShell } from "@/components/layout/app-shell";

import { LoginPage } from "@/features/auth/login-page";
import { SignupPage } from "@/features/auth/signup-page";
import { OnboardingPage } from "@/features/onboarding/onboarding-page";
import { ChatPage } from "@/features/chat/chat-page";
import { ThreadsPage } from "@/features/chat/threads-page";
import { SettingsPage } from "@/features/settings/settings-page";
import { UsagePage } from "@/features/usage/usage-page";

const rootRoute = createRootRoute({ component: () => <Outlet /> });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    const { accessToken } = useAuthStore.getState();
    throw redirect({ to: accessToken ? "/chat" : "/login" });
  },
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
});

const signupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/signup",
  component: SignupPage,
});

const protectedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "protected",
  beforeLoad: () => {
    const { accessToken } = useAuthStore.getState();
    if (!accessToken) {
      throw redirect({ to: "/login" });
    }
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

const onboardingRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/onboarding",
  component: OnboardingPage,
});

const chatRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/chat",
  component: ChatPage,
});

const chatThreadRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/chat/$threadId",
  component: ChatPage,
});

const threadsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/threads",
  component: ThreadsPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/settings",
  component: SettingsPage,
});

const usageRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/usage",
  component: UsagePage,
});

export const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  signupRoute,
  protectedRoute.addChildren([
    onboardingRoute,
    chatRoute,
    chatThreadRoute,
    threadsRoute,
    settingsRoute,
    usageRoute,
  ]),
]);
