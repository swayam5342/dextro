import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";

import { healthQuery } from "../queries";

function HealthBadge() {
  const { data, isPending, isError } = useQuery(healthQuery);

  if (isPending) {
    return <Badge variant="secondary">Checking…</Badge>;
  }
  if (isError || data.status !== "ok") {
    return <Badge variant="destructive">Unavailable</Badge>;
  }
  return <Badge>Healthy</Badge>;
}

export function StatusPage() {
  return (
    <div className="max-w-xl space-y-4">
      <h1 className="text-xl font-semibold tracking-tight">Server status</h1>
      <div className="flex items-center justify-between gap-4 rounded-2xl bg-card p-4">
        <div>
          <p className="font-medium">Database</p>
          <p className="text-sm text-muted-foreground">Whether this server can reach its database.</p>
        </div>
        <HealthBadge />
      </div>
    </div>
  );
}
