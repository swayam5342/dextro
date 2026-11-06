import { useQuery } from "@tanstack/react-query";

import { usageQuery } from "@/features/files/queries";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

import { poolQuery } from "../queries";

/** What's taking up the pool. Other is the rest of what Google counts, Gmail included. */
export function UsageCard({ className }: { className?: string }) {
  const { data: pool } = useQuery(poolQuery);
  const { data: usage } = useQuery(usageQuery);
  if (!pool) {
    return null;
  }
  const photo = usage?.photo_bytes ?? 0;
  const video = usage?.video_bytes ?? 0;
  const document = usage?.document_bytes ?? 0;
  const parts = [
    { label: "Photo", bytes: photo, color: "bg-usage-photo" },
    { label: "Video", bytes: video, color: "bg-usage-video" },
    { label: "Document", bytes: document, color: "bg-usage-document" },
    { label: "Other", bytes: Math.max(pool.used_bytes - photo - video - document, 0), color: "bg-usage-other" },
  ];
  const limit = pool.limit_bytes;

  return (
    <div className={cn("grid gap-2 text-[13px]", className)}>
      <ul className="grid gap-1">
        {parts.map(({ label, bytes, color }) => (
          <li key={label} className="flex items-center gap-2">
            <span className={cn("size-2 shrink-0 rounded-full", color)} />
            <span className="flex-1 text-muted-foreground">{label}</span>
            <span>{formatBytes(bytes)}</span>
          </li>
        ))}
        {pool.free_bytes !== null && (
          <li className="flex items-center gap-2">
            <span className="size-2 shrink-0 rounded-full bg-usage-free" />
            <span className="flex-1 text-muted-foreground">Free</span>
            <span>{formatBytes(pool.free_bytes)}</span>
          </li>
        )}
      </ul>
      <p className="flex items-baseline justify-between gap-2 border-t pt-2">
        <b className="font-semibold">{formatBytes(pool.used_bytes)} used</b>
        {limit !== null && <span className="text-muted-foreground">{formatBytes(limit)}</span>}
      </p>
      {limit !== null && limit > 0 && (
        <div
          role="img"
          aria-label={`${formatBytes(pool.used_bytes)} of ${formatBytes(limit)} used`}
          className="flex h-1.5 overflow-hidden rounded-full bg-raised"
        >
          {parts.map(({ label, bytes, color }) => (
            <span key={label} className={color} style={{ width: `${(bytes / limit) * 100}%` }} />
          ))}
        </div>
      )}
    </div>
  );
}
