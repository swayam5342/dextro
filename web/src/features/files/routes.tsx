import { createRoute } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { FilesPage } from "./components/files-page";
import { TrashPage } from "./components/trash-page";

export const filesRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/",
  validateSearch: (search): { folder?: string } => ({
    folder: typeof search.folder === "string" ? search.folder : undefined,
  }),
  component: function FilesRoute() {
    const { folder } = filesRoute.useSearch();
    // The key resets the page's state when moving between folders.
    return <FilesPage key={folder ?? "top"} folderId={folder} />;
  },
});

export const trashRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/trash",
  component: TrashPage,
});
