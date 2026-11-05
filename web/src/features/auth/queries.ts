import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";
import type { components } from "@/api/schema";

type Credentials = components["schemas"]["Credentials"];
type ChangePassword = components["schemas"]["ChangePassword"];

/** The signed-in user, or null for visitors. */
export const meQuery = queryOptions({
  queryKey: ["me"],
  queryFn: async () => {
    const { data, error, response } = await api.GET("/api/v1/auth/me");
    if (response.status === 401) {
      return null;
    }
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
  staleTime: 60_000,
  retry: false,
});

export const authConfigQuery = queryOptions({
  queryKey: ["auth-config"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/auth/config");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export const sessionsQuery = queryOptions({
  queryKey: ["sessions"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/auth/sessions");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: Credentials) => {
      const { data, error } = await api.POST("/api/v1/auth/login", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: (user) => queryClient.setQueryData(meQuery.queryKey, user),
  });
}

export function useRegister() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: Credentials) => {
      const { data, error } = await api.POST("/api/v1/auth/register", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: (user) => queryClient.setQueryData(meQuery.queryKey, user),
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST("/api/v1/auth/logout");
      // A 401 means the session already ended, which is what was asked for.
      if (!response.ok && response.status !== 401) {
        throw toApiError(error);
      }
    },
    // Nothing from this account may stay in memory for the next person.
    onSuccess: () => {
      queryClient.clear();
      queryClient.setQueryData(meQuery.queryKey, null);
    },
  });
}

export function useChangePassword() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: ChangePassword) => {
      const { error, response } = await api.POST("/api/v1/auth/password", { body });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionsQuery.queryKey }),
  });
}

export function useRevokeSession() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const { error, response } = await api.DELETE("/api/v1/auth/sessions/{id}", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionsQuery.queryKey }),
  });
}

export function useLinkGoogle() {
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/api/v1/auth/google/link");
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: ({ url }) => window.location.assign(url),
  });
}
