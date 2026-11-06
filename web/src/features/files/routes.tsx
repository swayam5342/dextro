import { createRoute } from "@tanstack/react-router";

import { authedRoute } from "@/features/auth/routes";

import { FilesPage } from "./components/files-page";
import { LinksPage } from "./components/links-page";
import { TrashPage } from "./components/trash-page";

export const filesRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/",
  validateSearch: (search): { folder?: string; q?: string } => ({
    folder: typeof search.folder === "string" ? search.folder : undefined,
    q: typeof search.q === "string" && search.q !== "" ? search.q : undefined,
  }),
  component: function FilesRoute() {
    const { folder, q } = filesRoute.useSearch();
    return <FilesPage folderId={folder} query={q} />;
  },
});

export const trashRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/trash",
  component: TrashPage,
});

export const linksRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/links",
  component: LinksPage,
});
