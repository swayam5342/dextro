import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";

import { trashQuery, useDeleteForever, useEmptyTrash, useRestore, type Item } from "../queries";

function TrashRow({ item, name, detail }: { item: Item; name: string; detail: string }) {
  const restore = useRestore();
  const destroy = useDeleteForever();

  function handleDelete() {
    const what = item.kind === "folder" ? "This folder and everything in it" : "This file";
    const confirmed = window.confirm(
      `Delete ${name} forever? ${what} will also be removed from your storage account. This can't be undone.`,
    );
    if (confirmed) {
      destroy.mutate(item);
    }
  }

  const busy = restore.isPending || destroy.isPending;
  return (
    <li className="space-y-2 py-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{name}</p>
          <p className="truncate text-sm text-muted-foreground">{detail}</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={busy} onClick={() => restore.mutate(item)}>
            Restore
          </Button>
          <Button variant="outline" size="sm" disabled={busy} onClick={handleDelete}>
            Delete forever
          </Button>
        </div>
      </div>
      <FormError message={restore.error?.message ?? destroy.error?.message} />
    </li>
  );
}

export function TrashPage() {
  const { data, isPending, isError } = useQuery(trashQuery);
  const empty = useEmptyTrash();
  const hasItems = data && data.files.length + data.folders.length > 0;

  function handleEmpty() {
    const confirmed = window.confirm(
      "Delete everything in the trash forever? It will also be removed from your storage accounts. This can't be undone.",
    );
    if (confirmed) {
      empty.mutate();
    }
  }

  return (
    <div className="max-w-3xl space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight">Trash</h1>
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost">
            <Link to="/" search={{}}>
              Back to files
            </Link>
          </Button>
          {hasItems && (
            <Button variant="outline" disabled={empty.isPending} onClick={handleEmpty}>
              {empty.isPending ? "Emptying…" : "Empty trash"}
            </Button>
          )}
        </div>
      </div>
      <p className="text-sm text-muted-foreground">
        Items here still use space in your storage accounts until you delete them forever.
      </p>
      <FormError message={empty.error?.message} />
      {isPending && <p className="text-sm text-muted-foreground">Loading…</p>}
      {isError && <FormError message="The trash couldn't be loaded. Reload the page to try again." />}
      {data && !hasItems && <p className="text-sm text-muted-foreground">The trash is empty.</p>}
      <ul className="divide-y">
        {data?.folders.map((folder) => (
          <TrashRow
            key={folder.id}
            item={{ kind: "folder", id: folder.id }}
            name={folder.name}
            detail="Folder and everything in it"
          />
        ))}
        {data?.files.map((file) => (
          <TrashRow
            key={file.id}
            item={{ kind: "file", id: file.id }}
            name={file.name}
            detail={formatBytes(file.size)}
          />
        ))}
      </ul>
    </div>
  );
}
