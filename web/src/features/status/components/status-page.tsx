import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

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
    <Card className="max-w-md">
      <CardHeader>
        <CardTitle className="flex items-center justify-between gap-4">
          Server status
          <HealthBadge />
        </CardTitle>
        <CardDescription>Whether this server can reach its database.</CardDescription>
      </CardHeader>
    </Card>
  );
}
