import { queryOptions } from "@tanstack/react-query";

import { api } from "@/api/client";

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
