import type { components } from "@/api/schema";
import { count, formatBytes } from "@/lib/format";

type Connection = components["schemas"]["Connection"];
type Pool = components["schemas"]["Pool"];
type PoolAccount = components["schemas"]["PoolAccount"];
type Routing = components["schemas"]["Routing"];
type RoutingMode = components["schemas"]["RoutingMode"];

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

/** Where an upload goes when no account was picked. */
export function routingText(mode: RoutingMode | undefined): string {
  switch (mode) {
    case "round_robin":
      return "the next account in turn";
    case "priority":
      return "the first account in your order with room";
    default:
      return "the account with the most free space";
  }
}

/** The account an upload goes to next: the one picked, or the one routing would choose. */
export function nextAccount(pool: Pool, routing: Routing | undefined, target: string): string | undefined {
  if (target) {
    return target;
  }
  const working = pool.accounts.filter((account) => account.available && !account.connection.draining);
  if (working.length < 2) {
    return undefined;
  }
  if (!routing || routing.mode === "most_space") {
    return working.reduce((best, account) => (room(account) > room(best) ? account : best)).connection.id;
  }
  const ordered = routing.order.filter((id) => working.some((account) => account.connection.id === id));
  const open = (id: string) => room(working.find((account) => account.connection.id === id)!) > 0;
  const start = routing.mode === "round_robin" ? ordered.indexOf(routing.last_connection_id ?? "") + 1 : 0;
  return [...ordered.slice(start), ...ordered.slice(0, start)].find(open);
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

const NEARLY_FULL = 0.9;

/** The one thing worth interrupting someone about, on any page. */
export function accountAlert(pool: Pool | undefined): { text: string; action: string } | undefined {
  const accounts = pool?.accounts ?? [];
  const broken = accounts.filter((account) => !account.available);
  const [first] = broken;
  if (first) {
    return {
      text:
        broken.length === 1
          ? `${first.connection.summary} can't be reached, so uploads are skipping it.`
          : `${broken.length} accounts can't be reached, so uploads are skipping them.`,
      action: broken.length === 1 ? "Reconnect" : "Reconnect them",
    };
  }
  const full = accounts.filter(({ available, connection, limit_bytes, used_bytes, reserved_bytes }) => (
    available && !connection.draining && limit_bytes !== null && used_bytes + reserved_bytes >= limit_bytes * NEARLY_FULL
  ));
  const [nearly] = full;
  if (nearly) {
    return {
      text: full.length === 1 ? `${nearly.connection.summary} is nearly full.` : `${full.length} accounts are nearly full.`,
      action: "See storage",
    };
  }
  return undefined;
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
