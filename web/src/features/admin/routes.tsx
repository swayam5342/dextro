import { createRoute, redirect } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { SettingsPage } from "./components/settings-page";

export const adminSettingsRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/admin/settings",
  // This only keeps people away from a page that would fail. The server is what
  // enforces the admin role.
  beforeLoad: ({ context }) => {
    if (context.user.role !== "admin") {
      throw redirect({ to: "/" });
    }
  },
  component: SettingsPage,
});
