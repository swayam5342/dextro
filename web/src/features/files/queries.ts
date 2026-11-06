import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";

const FILES_KEY = "files";
const TRASH_KEY = "trash";
const SHARES_KEY = "shares";
const USAGE_KEY = "usage";

/** Which kind of item an action applies to, and its ID. */
export interface Item {
  kind: "file" | "folder";
  id: string;
}

export function itemKey(item: Item): string {
  return `${item.kind}:${item.id}`;
}

export function folderQuery(folderId: string | undefined) {
  return queryOptions({
    queryKey: [FILES_KEY, folderId ?? "top"],
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/files", {
        params: { query: folderId ? { folder_id: folderId } : {} },
      });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
  });
}

export function searchQuery(query: string) {
  return queryOptions({
    queryKey: [FILES_KEY, "search", query],
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/search", { params: { query: { q: query } } });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
  });
}

async function listFiles(path: "/api/v1/recent" | "/api/v1/starred") {
  const { data, error } = await api.GET(path);
  if (!data) {
    throw toApiError(error);
  }
  return data;
}

export const recentQuery = queryOptions({
  queryKey: [FILES_KEY, "recent"],
  queryFn: () => listFiles("/api/v1/recent"),
});

export const starredQuery = queryOptions({
  queryKey: [FILES_KEY, "starred"],
  queryFn: () => listFiles("/api/v1/starred"),
});

export const trashQuery = queryOptions({
  queryKey: [TRASH_KEY],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/trash");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export const usageQuery = queryOptions({
  queryKey: [USAGE_KEY],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/usage");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export const sharesQuery = queryOptions({
  queryKey: [SHARES_KEY],
  // A folder opens often and this list changes rarely. What changes it refreshes it.
  staleTime: 30_000,
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/shares");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

/** Anything that changes files can change both the folder views and the trash. */
export function useRefreshFiles() {
  const queryClient = useQueryClient();
  return useCallback(
    () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: [FILES_KEY] }),
        queryClient.invalidateQueries({ queryKey: [TRASH_KEY] }),
        queryClient.invalidateQueries({ queryKey: [USAGE_KEY] }),
      ]),
    [queryClient],
  );
}

/** For changes that also reach the Links page: a new name, or a file that's gone. */
function useRefreshFilesAndLinks() {
  const queryClient = useQueryClient();
  const refreshFiles = useRefreshFiles();
  return () => Promise.all([refreshFiles(), queryClient.invalidateQueries({ queryKey: [SHARES_KEY] })]);
}

export async function createFolder(body: { name: string; parent_id: string | null }) {
  const { data, error } = await api.POST("/api/v1/folders", { body });
  if (!data) {
    throw toApiError(error);
  }
  return data;
}

export function useCreateFolder() {
  const refresh = useRefreshFiles();
  return useMutation({ mutationFn: createFolder, onSuccess: refresh });
}

export function useRename() {
  const refresh = useRefreshFilesAndLinks();
  return useMutation({
    mutationFn: async ({ item, name }: { item: Item; name: string }) => {
      const params = { path: { id: item.id } };
      const { error, response } =
        item.kind === "file"
          ? await api.PATCH("/api/v1/files/{id}", { params, body: { name } })
          : await api.PATCH("/api/v1/folders/{id}", { params, body: { name } });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useMove() {
  const refresh = useRefreshFiles();
  return useMutation({
    mutationFn: async ({ item, target }: { item: Item; target: string | null }) => {
      const params = { path: { id: item.id } };
      const { error, response } =
        item.kind === "file"
          ? await api.POST("/api/v1/files/{id}/move", { params, body: { folder_id: target } })
          : await api.POST("/api/v1/folders/{id}/move", { params, body: { parent_id: target } });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useMoveToTrash() {
  const refresh = useRefreshFilesAndLinks();
  return useMutation({
    mutationFn: async (item: Item) => {
      const params = { path: { id: item.id } };
      const { error, response } =
        item.kind === "file"
          ? await api.POST("/api/v1/files/{id}/trash", { params })
          : await api.POST("/api/v1/folders/{id}/trash", { params });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useRestore() {
  const refresh = useRefreshFiles();
  return useMutation({
    mutationFn: async (item: Item) => {
      const params = { path: { id: item.id } };
      const { error, response } =
        item.kind === "file"
          ? await api.POST("/api/v1/files/{id}/restore", { params })
          : await api.POST("/api/v1/folders/{id}/restore", { params });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useSetStarred() {
  const refresh = useRefreshFiles();
  return useMutation({
    mutationFn: async ({ id, starred }: { id: string; starred: boolean }) => {
      const options = { params: { path: { id } } };
      const { error, response } = await (starred
        ? api.PUT("/api/v1/files/{id}/star", options)
        : api.DELETE("/api/v1/files/{id}/star", options));
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useDeleteForever() {
  const refresh = useRefreshFilesAndLinks();
  return useMutation({
    mutationFn: async (item: Item) => {
      const params = { path: { id: item.id } };
      const { error, response } =
        item.kind === "file"
          ? await api.DELETE("/api/v1/files/{id}", { params })
          : await api.DELETE("/api/v1/folders/{id}", { params });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useSaveShare() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ fileId, ...body }: { fileId: string; role: "viewer" | "commenter" | "editor"; expires_at: string | null }) => {
      const { data, error } = await api.PUT("/api/v1/files/{id}/share", { params: { path: { id: fileId } }, body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [SHARES_KEY] }),
  });
}

export function useRemoveShare() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (fileId: string) => {
      const { error, response } = await api.DELETE("/api/v1/files/{id}/share", { params: { path: { id: fileId } } });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [SHARES_KEY] }),
  });
}

export function useEmptyTrash() {
  const refresh = useRefreshFilesAndLinks();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/v1/trash");
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function thumbnailUrl(fileId: string): string {
  return `/api/v1/files/${fileId}/thumbnail`;
}

function idsOf(items: Item[], kind: Item["kind"]): string[] {
  return items.filter((item) => item.kind === kind).map((item) => item.id);
}

export function zipUrl(items: Item[]): string {
  const query = new URLSearchParams();
  idsOf(items, "file").forEach((id) => query.append("file", id));
  idsOf(items, "folder").forEach((id) => query.append("folder", id));
  return `/api/v1/zip?${query.toString()}`;
}

/** Asks before downloading, because a refused download can't show its reason. */
export async function checkZip(items: Item[]): Promise<void> {
  const { error, response } = await api.GET("/api/v1/zip", {
    params: { query: { file: idsOf(items, "file"), folder: idsOf(items, "folder"), check: "true" } },
  });
  if (!response.ok) {
    throw toApiError(error);
  }
}

export function contentUrl(fileId: string, inline: boolean): string {
  const query = inline ? "?disposition=inline" : "";
  return `/api/v1/files/${fileId}/content${query}`;
}
