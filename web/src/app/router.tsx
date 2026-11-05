import { QueryClient } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";

import { statusRoute } from "@/features/status/routes";

import { rootRoute } from "./root-route";

export const queryClient = new QueryClient();

export const router = createRouter({
  routeTree: rootRoute.addChildren([statusRoute]),
  context: { queryClient },
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
