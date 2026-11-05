import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";
import type { components } from "@/api/schema";

type SettingsUpdate = components["schemas"]["SettingsUpdate"];

export const settingsQuery = queryOptions({
  queryKey: ["settings"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/admin/settings");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export function useUpdateSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: SettingsUpdate) => {
      const { data, error } = await api.PATCH("/api/v1/admin/settings", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: (settings) => queryClient.setQueryData(settingsQuery.queryKey, settings),
  });
}

export const usersQuery = queryOptions({
  queryKey: ["admin-users"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/admin/users");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: { email: string; password: string }) => {
      const { data, error } = await api.POST("/api/v1/admin/users", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersQuery.queryKey }),
  });
}

export function useResetUserPassword() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, password }: { id: string; password: string }) => {
      const { error, response } = await api.POST("/api/v1/admin/users/{id}/password", {
        params: { path: { id } },
        body: { password },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersQuery.queryKey }),
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const { error, response } = await api.DELETE("/api/v1/admin/users/{id}", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersQuery.queryKey }),
  });
}
