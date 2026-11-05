import { QueryClient } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";

import { adminSettingsRoute } from "@/features/admin/routes";
import { accountRoute, authedRoute, registerRoute, signInRoute } from "@/features/auth/routes";
import { filesRoute, trashRoute } from "@/features/files/routes";
import { statusRoute } from "@/features/status/routes";
import { storageRoute } from "@/features/storage/routes";

import { rootRoute } from "./root-route";

export const queryClient = new QueryClient();

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    signInRoute,
    registerRoute,
    authedRoute.addChildren([
      filesRoute,
      trashRoute,
      statusRoute,
      storageRoute,
      accountRoute,
      adminSettingsRoute,
    ]),
  ]),
  context: { queryClient },
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
