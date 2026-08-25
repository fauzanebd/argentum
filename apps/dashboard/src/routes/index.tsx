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
import { WatchersPage } from "@/features/watchers/watchers-page";
import { SharePage } from "@/features/share/share-page";
import { DashboardSharePage } from "@/features/share/dashboard-share-page";
import { DocumentsPage } from "@/features/documents/documents-page";
import {
  DashboardDetailPage,
  DashboardsPage,
} from "@/features/dashboards/dashboards-page";
import { KnowledgePage } from "@/features/knowledge/knowledge-page";
import { DocumentReviewPage } from "@/features/knowledge/document-review";

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

// The report player (T-V4). Public by design, and for the same reason the
// invite route above is: the token in the path is the only credential the
// visitor holds, and they have no account for a session to belong to.
//
// It is deliberately *outside* `protectedRoute`, so it renders no AppShell —
// no sidebar, no navigation, nothing that implies the visitor is inside
// somebody's workspace. They were sent one report and that is what they get.
const shareRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$token",
  component: function SharedReport() {
    const { token } = shareRoute.useParams();
    return <SharePage token={token} />;
  },
});

// The shared dashboard (T-D13/T-D21). Public, outside `protectedRoute`, and
// for the same reasons the report player above is — with one difference worth
// naming: this page runs live SQL against a customer's warehouse when it opens,
// where the player replays a plan that was already produced.
const dashboardShareRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/d/$token",
  component: function SharedDashboard() {
    const { token } = dashboardShareRoute.useParams();
    return <DashboardSharePage token={token} />;
  },
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

const watchersRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/watchers",
  component: WatchersPage,
});

const documentsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/documents",
  component: DocumentsPage,
});

// Native dashboards (T-D10). The detail route is what the link in a chat reply
// points at, so it has to exist even though the chat renders the panels inline:
// somebody opening the dashboard a week later has no transcript to read it in.
// Uploaded documents and their review (T-P1/T-P7). `/knowledge`, not
// `/documents`: that path is the documents this product *generates*, and the
// two are opposites — output addressed by thread against input a tenant
// supplies.
const knowledgeRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/knowledge",
  component: KnowledgePage,
});

const knowledgeDetailRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/knowledge/$id",
  component: DocumentReviewPage,
});

const dashboardsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/dashboards",
  component: DashboardsPage,
});

const dashboardDetailRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: "/dashboards/$id",
  component: DashboardDetailPage,
});

export const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  signupRoute,
  acceptInviteRoute,
  shareRoute,
  dashboardShareRoute,
    protectedRoute.addChildren([
      onboardingRoute,
      chatRoute,
      chatThreadRoute,
      settingsRoute,
      usageRoute,
      scheduledTasksRoute,
      watchersRoute,
      documentsRoute,
      knowledgeRoute,
      knowledgeDetailRoute,
      dashboardsRoute,
      dashboardDetailRoute,
    ]),
]);
