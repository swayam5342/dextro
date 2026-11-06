import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatDate } from "@/lib/format";

import { sharesQuery } from "../queries";
import { CopyButton, ShareDialog } from "./share-dialog";

type Share = components["schemas"]["Share"];

const ROLE_LABELS: Record<Share["role"], string> = {
  viewer: "Anyone with the link can view",
  commenter: "Anyone with the link can comment",
  editor: "Anyone with the link can edit",
};

function expiryText(share: Share): string {
  if (!share.expires_at) {
    return "No expiry";
  }
  if (Date.parse(share.expires_at) <= Date.now()) {
    return "Past its expiry. dextro is still trying to remove it, so it may still work.";
  }
  return `Ends ${formatDate(share.expires_at)}`;
}

/** Every public link in one place, so nothing stays public by being forgotten. */
export function LinksPage() {
  const { data, isPending, isError } = useQuery(sharesQuery);
  const [managing, setManaging] = useState<Share | null>(null);

  return (
    <div className="max-w-3xl space-y-[18px]">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Google links</h1>
        <p className="text-muted-foreground">Anyone who has one of these links can open the file without signing in.</p>
      </div>
      {isPending && <p className="text-muted-foreground">Loading your links…</p>}
      {isError && <FormError message="Your links couldn't be loaded. Reload the page to try again." />}
      {data && data.shares.length === 0 && (
        <p className="py-6 text-muted-foreground">
          {data.enabled
            ? "No file has a link. Make one from a file's menu."
            : "The admin hasn't turned on Google links for this server."}
        </p>
      )}
      {data && data.shares.length > 0 && (
        <ul className="grid gap-2">
          {data.shares.map((share) => (
            <li key={share.file_id} className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[14px] bg-card p-3.5">
              <span className="min-w-0 flex-1 basis-56">
                <span className="block truncate font-medium">{share.name}</span>
                <span className="block text-[13px] text-muted-foreground">{ROLE_LABELS[share.role]}</span>
                <span className="block text-xs text-faint">{expiryText(share)}</span>
              </span>
              <CopyButton text={share.url} label={`Copy the link to ${share.name}`} />
              <Button
                variant="outline"
                size="sm"
                className="bg-raised hover:bg-[#3a3a3a]"
                aria-label={`Manage the link to ${share.name}`}
                onClick={() => setManaging(share)}
              >
                Manage
              </Button>
            </li>
          ))}
        </ul>
      )}
      {managing && <ShareDialog fileId={managing.file_id} name={managing.name} onClose={() => setManaging(null)} />}
    </div>
  );
}
