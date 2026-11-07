import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";

import { duplicatesQuery, useMoveToTrash } from "../queries";
import { fileIcon, fileTint } from "./file-icon";

type File = components["schemas"]["File"];
type DuplicateFile = components["schemas"]["DuplicateFile"];

function Copy({ copied, onTrash }: { copied: DuplicateFile; onTrash: (file: File) => void }) {
  const { file, location } = copied;
  const Icon = fileIcon(file.content_type);
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-2 py-2">
      <Icon className={`size-[18px] shrink-0 ${fileTint(file.content_type)}`} />
      <span className="min-w-0 flex-1 basis-48">
        <span className="block truncate">{file.name}</span>
        <Link
          to="/"
          search={file.folder_id ? { folder: file.folder_id } : {}}
          className="block truncate text-[13px] text-muted-foreground hover:text-foreground"
        >
          {location === "" ? "Files" : location.replaceAll("/", " / ")}
        </Link>
      </span>
      <Button variant="soft" size="sm" aria-label={`Move ${file.name} to the trash`} onClick={() => onTrash(file)}>
        Trash
      </Button>
    </li>
  );
}

/** Files with the same content, found by their hash, so nothing has to be downloaded. */
export function DuplicatesPage() {
  const { data, isPending, isError } = useQuery(duplicatesQuery);
  const trash = useMoveToTrash();
  const [asking, setAsking] = useState<File | null>(null);

  return (
    <div className="max-w-3xl space-y-[18px]">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Duplicates</h1>
        <p className="text-muted-foreground">
          {data && data.groups.length > 0
            ? `Keeping one of each would free ${formatBytes(data.reclaimable_bytes)}.`
            : "Files with exactly the same content, wherever they are."}
        </p>
      </div>
      {isPending && <p className="text-muted-foreground">Looking for duplicates…</p>}
      {isError && <FormError message="Your duplicates couldn't be loaded. Reload the page to try again." />}
      {data && data.groups.length === 0 && <p className="py-6 text-muted-foreground">No duplicates found.</p>}
      {data && data.groups.length > 0 && (
        <ul className="grid gap-2">
          {data.groups.map((group) => (
            <li key={`${group.hash}:${group.size}`} className="rounded-[14px] bg-card px-3.5 py-2.5">
              <p className="text-xs text-faint">
                {group.files.length} copies, {formatBytes(group.size)} each
              </p>
              <ul className="divide-y">
                {group.files.map((copied) => (
                  <Copy key={copied.file.id} copied={copied} onTrash={setAsking} />
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
      {asking && (
        <ConfirmDialog
          title={`Move ${asking.name} to the trash?`}
          text="You can restore it from the trash. Any Google link on this copy will end, and restoring won't bring it back."
          action="Move to trash"
          pending={trash.isPending}
          error={trash.error?.message}
          onClose={() => setAsking(null)}
          onConfirm={() => trash.mutate({ kind: "file", id: asking.id }, { onSuccess: () => setAsking(null) })}
        />
      )}
    </div>
  );
}
