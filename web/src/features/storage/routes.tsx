import { createRoute } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { StoragePage } from "./components/storage-page";

function textParam(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

export const storageRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/storage",
  validateSearch: (search): { error?: string; connected?: string } => ({
    error: textParam(search.error),
    connected: textParam(search.connected),
  }),
  component: function StorageRoute() {
    const { error, connected } = storageRoute.useSearch();
    return <StoragePage redirectError={error} connected={connected} />;
  },
});
