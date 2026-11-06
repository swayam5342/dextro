import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { toApiError } from "@/api/errors";
import { sharesQuery, useRefreshFiles } from "@/features/files/queries";

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

export const routingQuery = queryOptions({
  queryKey: ["upload-routing"],
  queryFn: async () => {
    const { data, error } = await api.GET("/api/v1/storage/routing");
    if (!data) {
      throw toApiError(error);
    }
    return data;
  },
});

type Routing = components["schemas"]["Routing"];
type RoutingUpdate = components["schemas"]["RoutingUpdate"];

/** The choice shows at once, and saves take turns so a quick run of moves lands in order. */
export function useSetRouting() {
  const queryClient = useQueryClient();
  return useMutation({
    scope: { id: "upload-routing" },
    mutationFn: async (body: RoutingUpdate) => {
      const { data, error } = await api.PUT("/api/v1/storage/routing", { body });
      if (!data) {
        throw toApiError(error);
      }
      return data;
    },
    onMutate: async (body) => {
      // A read already on its way would put the old choice back.
      await queryClient.cancelQueries({ queryKey: routingQuery.queryKey });
      queryClient.setQueryData<Routing>(routingQuery.queryKey, (old) => old && { ...old, ...body });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: routingQuery.queryKey }),
  });
}

type TransferQueue = components["schemas"]["TransferQueue"];

function isMoving(queue: TransferQueue | undefined): boolean {
  return Boolean(queue && (queue.waiting > 0 || queue.removing > 0));
}

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
  refetchInterval: (query) => (isMoving(query.state.data) ? 3000 : false),
});

/** Everything a change to an account can touch. */
function useRefreshAccounts() {
  const queryClient = useQueryClient();
  const refreshFiles = useRefreshFiles();
  return () =>
    Promise.all([
      refreshFiles(),
      ...[connectionsQuery, poolQuery, routingQuery, transfersQuery, sharesQuery].map(({ queryKey }) => queryClient.invalidateQueries({ queryKey })),
    ]);
}

/**
 * Files change account in the background, so what's on screen follows the queue. This
 * runs on every page, so it asks less often than the Storage page does. Reading the pool
 * asks Google about every account, so that waits until the moves are done.
 */
export function useFollowTransfers() {
  const queryClient = useQueryClient();
  const refreshFiles = useRefreshFiles();
  const { data: left } = useQuery({
    ...transfersQuery,
    select: (queue) => queue.waiting + queue.removing,
    refetchInterval: (query) => (isMoving(query.state.data) ? 10_000 : false),
  });
  const previous = useRef(left);
  useEffect(() => {
    if (left !== undefined && previous.current !== undefined && left < previous.current) {
      void refreshFiles();
      if (left === 0) {
        void queryClient.invalidateQueries({ queryKey: poolQuery.queryKey });
      }
    }
    previous.current = left;
  }, [left, queryClient, refreshFiles]);
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

async function syncConnection(id: string) {
  const { data, error } = await api.POST("/api/v1/storage/connections/{id}/sync", { params: { path: { id } } });
  if (!data) {
    throw toApiError(error);
  }
  return data;
}

export function useSyncConnection(id: string) {
  const refresh = useRefreshAccounts();
  return useMutation({ mutationFn: () => syncConnection(id), onSuccess: refresh });
}

/** Syncs every account at once. One that fails doesn't stop the others. */
export function useSyncAccounts() {
  const refresh = useRefreshAccounts();
  const { data: connections } = useQuery(connectionsQuery);
  return useMutation({
    mutationFn: async () => {
      const results = await Promise.allSettled((connections ?? []).map(({ id }) => syncConnection(id)));
      const total = { added: 0, removed: 0, failed: 0 };
      for (const result of results) {
        if (result.status === "fulfilled") {
          total.added += result.value.added;
          total.removed += result.value.removed;
        } else {
          total.failed += 1;
        }
      }
      return total;
    },
    onSuccess: refresh,
  });
}

export function useRemoveConnection(id: string) {
  const refresh = useRefreshAccounts();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/v1/storage/connections/{id}", {
        params: { path: { id } },
      });
      if (!response.ok) {
        throw toApiError(error);
      }
    },
    onSuccess: refresh,
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
