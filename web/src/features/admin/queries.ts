import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { toApiError } from "@/api/errors";
import type { components } from "@/api/schema";
import { authConfigQuery } from "@/features/auth/queries";

type Settings = components["schemas"]["Settings"];

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
    mutationFn: async (body: Settings) => {
      const { data, error } = await api.PATCH("/api/v1/admin/settings", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: (settings) => {
      queryClient.setQueryData(settingsQuery.queryKey, settings);
      return queryClient.invalidateQueries({ queryKey: authConfigQuery.queryKey });
    },
  });
}
