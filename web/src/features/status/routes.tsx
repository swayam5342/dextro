import { createRoute } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { StatusPage } from "./components/status-page";

export const statusRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/status",
  component: StatusPage,
});
