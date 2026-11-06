import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";

export const connectionsQuery = queryOptions({
  queryKey: ["storage-connections"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/storage/connections");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export const poolQuery = queryOptions({
  queryKey: ["storage-pool"],
  // Each read asks Google, and uploads and removals refresh it themselves.
  staleTime: 30_000,
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/storage/pool");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export function useTestConnection(id: string) {
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST("/api/v1/storage/connections/{id}/test", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
  });
}

export function useSyncConnection(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/api/v1/storage/connections/{id}/sync", {
        params: { path: { id } },
      });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ["files"] }),
        queryClient.invalidateQueries({ queryKey: ["trash"] }),
        queryClient.invalidateQueries({ queryKey: poolQuery.queryKey }),
      ]),
  });
}

export function useRemoveConnection(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/v1/storage/connections/{id}", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: connectionsQuery.queryKey }),
        queryClient.invalidateQueries({ queryKey: poolQuery.queryKey }),
      ]),
  });
}

export function useConnectGoogleDrive() {
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/api/v1/storage/google/connect");
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: ({ url }) => window.location.assign(url),
  });
}
