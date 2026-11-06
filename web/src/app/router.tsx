import { QueryClient } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";

import {
  adminAuditRoute,
  adminSettingsRoute,
  adminStatusRoute,
  adminUsersRoute,
} from "@/features/admin/routes";
import {
  accountRoute,
  aliasRoutes,
  authedRoute,
  changePasswordRoute,
  registerRoute,
  signInRoute,
} from "@/features/auth/routes";
import { filesRoute, linksRoute, trashRoute } from "@/features/files/routes";
import { storageRoute } from "@/features/storage/routes";

import { rootRoute } from "./root-route";

export const queryClient = new QueryClient();

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    signInRoute,
    registerRoute,
    changePasswordRoute,
    ...aliasRoutes,
    authedRoute.addChildren([
      filesRoute,
      trashRoute,
      linksRoute,
      storageRoute,
      accountRoute,
      adminSettingsRoute,
      adminUsersRoute,
      adminStatusRoute,
      adminAuditRoute,
    ]),
  ]),
  context: { queryClient },
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
