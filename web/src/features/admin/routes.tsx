import { createRoute, redirect } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { AuditPage } from "./components/audit-page";
import { SettingsPage } from "./components/settings-page";
import { StatusPage } from "./components/status-page";
import { UsersPage } from "./components/users-page";

// This only keeps people away from pages that would fail. The server is what
// enforces the admin role.
function adminOnly({ context }: { context: { user: { role: string } } }) {
  if (context.user.role !== "admin") {
    throw redirect({ to: "/" });
  }
}

export const adminSettingsRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admin/settings",
  beforeLoad: adminOnly,
  component: SettingsPage,
});

export const adminUsersRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admin/users",
  beforeLoad: adminOnly,
  component: UsersPage,
});

export const adminStatusRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admin/status",
  beforeLoad: adminOnly,
  component: StatusPage,
});

export const adminAuditRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admin/audit",
  beforeLoad: adminOnly,
  component: AuditPage,
});
