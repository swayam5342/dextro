import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef } from "react";

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

export const transfersQuery = queryOptions({
  queryKey: ["transfers"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/transfers");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
  // Only while something's moving. Moves run in the background, so nothing else says.
  refetchInterval: (query) => {
    const queue = query.state.data;
    return queue && (queue.waiting > 0 || queue.removing > 0) ? 3000 : false;
  },
});

/** Everything a move can change. */
function useRefreshAccounts() {
  const queryClient = useQueryClient();
  return useCallback(
    () =>
      Promise.all(
        [["files"], ["trash"], connectionsQuery.queryKey, poolQuery.queryKey, transfersQuery.queryKey].map((queryKey) =>
          queryClient.invalidateQueries({ queryKey }),
        ),
      ),
    [queryClient],
  );
}

/** Files change account in the background, so what's on screen follows the queue. */
export function useFollowTransfers() {
  const { data } = useQuery(transfersQuery);
  const refresh = useRefreshAccounts();
  const left = data ? data.waiting + data.removing : undefined;
  const previous = useRef(left);
  useEffect(() => {
    if (left !== undefined && previous.current !== undefined && left < previous.current) {
      void refresh();
    }
    previous.current = left;
  }, [left, refresh]);
}

export function useTransferFiles() {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async (body: { file_ids: string[]; target_connection_id: string }) => {
      const { data, error } = await api.POST("/api/v1/transfers", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: refresh,
  });
}

export function useEmptyConnection(id: string) {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/api/v1/storage/connections/{id}/empty", {
        params: { path: { id } },
      });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onSuccess: refresh,
  });
}

export function useStopEmptying(id: string) {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/v1/storage/connections/{id}/empty", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useRetryTransfers() {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST("/api/v1/transfers/retry");
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
  });
}

export function useCancelTransfer() {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async (id: string) => {
      const { error, response } = await api.DELETE("/api/v1/transfers/{id}", { params: { path: { id } } });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
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
        queryClient.invalidateQueries({ queryKey: transfersQuery.queryKey }),
        queryClient.invalidateQueries({ queryKey: ["files"] }),
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
