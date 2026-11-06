import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDateTime } from "@/lib/format";

import { auditQuery } from "../queries";

type AuditEvent = components["schemas"]["AuditEvent"];

/** Every action the server records, named the way a person would say it. */
const ACTIONS: Record<string, string> = {
  "auth.login": "Signed in",
  "auth.login_failed": "Failed sign-in",
  "auth.logout": "Signed out",
  "auth.password_changed": "Changed password",
  "auth.session_revoked": "Signed out a device",
  "auth.google_linked": "Linked Google sign-in",
  "user.registered": "Created the admin account",
  "user.created": "Added a user",
  "user.deleted": "Deleted a user",
  "user.password_reset": "Set a user's password",
  "user.role_changed": "Changed the admin",
  "settings.updated": "Changed server settings",
  "storage.connected": "Linked a storage account",
  "storage.removed": "Removed a storage account",
  "storage.synced": "Synced with Drive",
  "storage.picker_opened": "Opened Add from Drive",
  "storage.files_added": "Added files from Drive",
  "api_key.created": "Created an API key",
  "api_key.revoked": "Revoked an API key",
  "file.deleted": "Deleted a file forever",
  "folder.deleted": "Deleted a folder forever",
};

function details(event: AuditEvent): string {
  return Object.entries(event.metadata)
    .map(([key, value]) =>
      key.endsWith("_bytes") && typeof value === "number"
        ? `${key.replace("_bytes", "").replaceAll("_", " ")}: ${formatBytes(value)}`
        : `${key.replaceAll("_", " ")}: ${String(value)}`,
    )
    .join(", ");
}

function who(event: AuditEvent): string {
  if (event.actor_email) {
    return event.actor_email;
  }
  return event.ip === "cli" || event.ip === "migration" ? "Server command" : "Unknown";
}

function EventRow({ event }: { event: AuditEvent }) {
  const cell = "border-t px-3 py-2.5 text-muted-foreground";
  const extra = details(event);
  return (
    <tr>
      <td className={`${cell} whitespace-nowrap`}>{formatDateTime(event.created_at)}</td>
      <td className={`${cell} w-full max-w-0 text-foreground`}>
        <span className="block truncate font-medium">{ACTIONS[event.action] ?? event.action}</span>
        {extra && <span className="block truncate text-xs text-faint" title={extra}>{extra}</span>}
        <span className="block truncate text-xs text-faint md:hidden">{who(event)}</span>
      </td>
      <td className={`${cell} hidden max-w-56 truncate md:table-cell`}>{who(event)}</td>
      <td className={`${cell} hidden whitespace-nowrap lg:table-cell`}>{event.ip}</td>
    </tr>
  );
}

export function AuditPage() {
  const [action, setAction] = useState("");
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } = useInfiniteQuery(auditQuery(action));
  const events = data?.pages.flatMap((page) => page.events) ?? [];

  return (
    <div className="max-w-5xl">
      <div className="mb-[18px] flex min-h-9 flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h1 className="text-xl font-semibold tracking-tight">Audit log</h1>
        <select
          aria-label="Show only"
          className="h-9 max-w-full rounded-full bg-card px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          value={action}
          onChange={(event) => setAction(event.target.value)}
        >
          <option value="">Everything</option>
          {Object.entries(ACTIONS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      </div>
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && <FormError message="The audit log couldn't be loaded. Reload the page to try again." />}
      {data && events.length === 0 && <p className="py-10 text-muted-foreground">Nothing has been recorded here yet.</p>}
      {events.length > 0 && (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="text-left text-xs text-faint">
              <th className="px-3 pb-2 font-normal">When</th>
              <th className="px-3 pb-2 font-normal">What</th>
              <th className="hidden px-3 pb-2 font-normal md:table-cell">Who</th>
              <th className="hidden px-3 pb-2 font-normal lg:table-cell">Address</th>
            </tr>
          </thead>
          <tbody>
            {events.map((event) => (
              <EventRow key={event.id} event={event} />
            ))}
          </tbody>
        </table>
      )}
      {hasNextPage && (
        <Button variant="outline" className="mt-4" disabled={isFetchingNextPage} onClick={() => void fetchNextPage()}>
          {isFetchingNextPage ? "Loading…" : "Show older events"}
        </Button>
      )}
    </div>
  );
}
