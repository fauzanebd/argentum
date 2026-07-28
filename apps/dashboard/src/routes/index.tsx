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
import { AcceptInvitePage } from "@/features/auth/accept-invite-page";
import { OnboardingPage } from "@/features/onboarding/onboarding-page";
import { ChatPage } from "@/features/chat/chat-page";
import { SettingsPage } from "@/features/settings/settings-page";
import { UsagePage } from "@/features/usage/usage-page";
import { ScheduledTasksPage } from "@/features/scheduled-tasks/scheduled-tasks-page";

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

// Public by design: the invitee has no session until they accept, and the
// token in the query string is the only credential they hold.
const acceptInviteRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/accept-invite",
  component: AcceptInvitePage,
  validateSearch: (search: Record<string, unknown>) => ({
    token: typeof search.token === "string" ? search.token : undefined,
  }),
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

const scheduledTasksRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/scheduled-tasks",
  component: ScheduledTasksPage,
  validateSearch: (search: Record<string, unknown>) => ({
    taskId: typeof search.taskId === "string" ? search.taskId : undefined,
  }),
});

export const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  signupRoute,
  acceptInviteRoute,
    protectedRoute.addChildren([
      onboardingRoute,
      chatRoute,
      chatThreadRoute,
      settingsRoute,
      usageRoute,
      scheduledTasksRoute,
    ]),
]);
