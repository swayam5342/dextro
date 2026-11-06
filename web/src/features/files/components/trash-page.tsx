import { useQuery } from "@tanstack/react-query";
import { Folder, type LucideIcon } from "lucide-react";
import { useState } from "react";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDate } from "@/lib/format";

import { trashQuery, useDeleteForever, useEmptyTrash, useRestore, type Item } from "../queries";
import { fileIcon } from "./file-icon";

interface TrashRowProps {
  item: Item;
  name: string;
  detail: string;
  icon: LucideIcon;
  /** Someone's own Drive file, which stays in Drive. */
  external?: boolean;
}

function TrashRow({ item, name, detail, icon: Icon, external = false }: TrashRowProps) {
  const restore = useRestore();
  const destroy = useDeleteForever();
  const [confirming, setConfirming] = useState(false);
  const busy = restore.isPending || destroy.isPending;
  const what = item.kind === "folder" ? "This folder and everything in it" : "This file";
  const warning = external
    ? "This removes it from Tributary. The file stays in your Google Drive."
    : `${what} will also be removed from your storage account. This can't be undone.`;

  return (
    <li className="space-y-2 border-t py-2.5 first:border-t-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="grid size-[38px] shrink-0 place-items-center rounded-[7px] bg-card">
          <Icon className="size-[18px] text-muted-foreground" />
        </span>
        <div className="min-w-0 flex-1 basis-40 leading-tight">
          <p className="truncate font-medium">{name}</p>
          <p className="truncate text-xs text-faint">{detail}</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={busy} onClick={() => restore.mutate(item)}>
            Restore
          </Button>
          <Button variant="destructive" size="sm" disabled={busy} onClick={() => setConfirming(true)}>
            {external ? "Remove from Tributary" : "Delete forever"}
          </Button>
        </div>
      </div>
      <FormError message={restore.error?.message} />
      {confirming && (
        <ConfirmDialog
          title={external ? `Remove ${name} from Tributary?` : `Delete ${name} forever?`}
          text={warning}
          action={external ? "Remove from Tributary" : "Delete forever"}
          pending={destroy.isPending}
          error={destroy.error?.message}
          onConfirm={() => destroy.mutate(item, { onSuccess: () => setConfirming(false) })}
          onClose={() => {
            setConfirming(false);
            destroy.reset();
          }}
        />
      )}
    </li>
  );
}

export function TrashPage() {
  const { data, isPending, isError } = useQuery(trashQuery);
  const empty = useEmptyTrash();
  const [confirming, setConfirming] = useState(false);
  const hasItems = data && data.files.length + data.folders.length > 0;

  return (
    <div className="max-w-3xl">
      <div className="mb-1 flex min-h-9 items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight">Trash</h1>
        {hasItems && (
          <Button variant="destructive" onClick={() => setConfirming(true)}>
            Empty trash
          </Button>
        )}
      </div>
      <p className="mb-[18px] text-muted-foreground">
        Items here still use space in your accounts until you delete them forever.
      </p>
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && <FormError message="The trash couldn't be loaded. Reload the page to try again." />}
      {data && !hasItems && <p className="py-10 text-muted-foreground">The trash is empty.</p>}
      <ul>
        {data?.folders.map((folder) => (
          <TrashRow
            key={folder.id}
            item={{ kind: "folder", id: folder.id }}
            name={folder.name}
            detail="Folder and everything in it"
            icon={Folder}
          />
        ))}
        {data?.files.map((file) => (
          <TrashRow
            key={file.id}
            item={{ kind: "file", id: file.id }}
            name={file.name}
            detail={`${formatBytes(file.size)}, added ${formatDate(file.created_at)}`}
            icon={fileIcon(file.content_type)}
            external={file.external}
          />
        ))}
      </ul>
      {confirming && (
        <ConfirmDialog
          title="Empty the trash?"
          text="Files Tributary stored will also be removed from your storage accounts, and files you added from Drive stay in Drive. This can't be undone."
          action="Empty trash"
          pending={empty.isPending}
          error={empty.error?.message}
          onConfirm={() => empty.mutate(undefined, { onSuccess: () => setConfirming(false) })}
          onClose={() => {
            setConfirming(false);
            empty.reset();
          }}
        />
      )}
    </div>
  );
}
