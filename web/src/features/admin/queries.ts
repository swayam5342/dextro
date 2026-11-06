import { infiniteQueryOptions, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

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

export function useRunJob() {
  return useMutation({
    mutationFn: async (name: string) => {
      const { error, response } = await api.POST("/api/v1/admin/jobs/{name}/run", { params: { path: { name } } });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
  });
}

export const statusQuery = queryOptions({
  queryKey: ["admin-status"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/admin/status");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export function auditQuery(action: string) {
  return infiniteQueryOptions({
    queryKey: ["admin-audit", action],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/api/v1/admin/audit", {
        params: { query: { action: action || undefined, before: pageParam } },
      });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    getNextPageParam: (page) => page.next_before ?? undefined,
  });
}

export const healthQuery = queryOptions({
  queryKey: ["health"],
  queryFn: async () => {
    const { data, error } = await api.GET("/health");
    // The server answers 503 with the same body when it can't reach Postgres.
    const health = data ?? error;
    if (!health) {
      throw new Error("Empty health response");
    }
    return health;
  },
  refetchInterval: 30_000,
  retry: false,
});
