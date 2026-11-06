import type { components } from "@/api/schema";
import { count, formatBytes } from "@/lib/format";

type Connection = components["schemas"]["Connection"];
type PoolAccount = components["schemas"]["PoolAccount"];

/** Accounts that can take new files. One being emptied can't. */
export function usableConnections(connections: Connection[] | undefined): Connection[] {
  return connections?.filter((connection) => !connection.draining) ?? [];
}

/** How much room an account has. No limit is the most room there is. */
export function room(account: PoolAccount): number {
  if (account.limit_bytes === null) {
    return Number.POSITIVE_INFINITY;
  }
  return Math.max(account.limit_bytes - account.used_bytes - account.reserved_bytes, 0);
}

export function freeSpaceText(account: PoolAccount | undefined): string | undefined {
  if (!account) {
    return undefined;
  }
  if (!account.available) {
    return "Can't be reached";
  }
  if (account.limit_bytes === null) {
    return "No limit";
  }
  return `${formatBytes(room(account))} free`;
}

/** What a sync did, in words. */
export function syncText({ added, removed, failed = 0 }: { added: number; removed: number; failed?: number }): string {
  const parts = [];
  if (added > 0) {
    parts.push(`${count(added, "file")} found`);
  }
  if (removed > 0) {
    parts.push(`${count(removed, "file")} gone from Drive`);
  }
  if (failed > 0) {
    parts.push(`${count(failed, "account")} couldn't be checked`);
  }
  return parts.length === 0 ? "Already up to date." : `${parts.join(", ")}.`;
}
