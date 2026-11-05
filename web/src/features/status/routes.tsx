import { createRoute } from "@tanstack/react-router";

import { rootRoute } from "@/app/root-route";

import { StatusPage } from "./components/status-page";

export const statusRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: StatusPage,
});
