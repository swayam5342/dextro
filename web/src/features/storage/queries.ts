import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";
import type { components } from "@/api/schema";

type S3ConnectionInput = components["schemas"]["S3ConnectionInput"];

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

export function useCreateS3Connection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: S3ConnectionInput) => {
      const { data, error } = await api.POST("/api/v1/storage/connections/s3", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectionsQuery.queryKey }),
  });
}

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
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectionsQuery.queryKey }),
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
