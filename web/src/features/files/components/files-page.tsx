import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from "react";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { connectionsQuery } from "@/features/storage/queries";
import { formatBytes, formatDate } from "@/lib/format";

import {
  contentUrl,
  folderQuery,
  searchQuery,
  useCreateFolder,
  useMoveToTrash,
  useRename,
  type Item,
} from "../queries";
import { useUploads, type Upload } from "../uploads";
import { MoveDialog, NameDialog, PreviewDialog } from "./dialogs";

type File = components["schemas"]["File"];
type Folder = components["schemas"]["Folder"];

type Action =
  | { type: "rename"; item: Item; name: string }
  | { type: "move"; item: Item; name: string }
  | { type: "preview"; file: File }
  | { type: "new-folder" };

function Breadcrumbs({ path }: { path: Folder[] }) {
  return (
    <nav aria-label="Folders" className="flex min-w-0 flex-wrap items-center gap-x-2 text-2xl font-semibold tracking-tight">
      {path.length === 0 ? (
        <h1>Files</h1>
      ) : (
        <Link to="/" search={{}} className="text-muted-foreground hover:text-foreground">
          Files
        </Link>
      )}
      {path.map((folder, index) => (
        <span key={folder.id} className="flex min-w-0 items-center gap-2">
          <span className="text-muted-foreground">/</span>
          {index === path.length - 1 ? (
            <h1 className="truncate">{folder.name}</h1>
          ) : (
            <Link
              to="/"
              search={{ folder: folder.id }}
              className="truncate text-muted-foreground hover:text-foreground"
            >
              {folder.name}
            </Link>
          )}
        </span>
      ))}
    </nav>
  );
}

function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label={`Actions for ${label}`}>
          Actions
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>{children}</DropdownMenuContent>
    </DropdownMenu>
  );
}

function Row({ name, detail, menu }: { name: ReactNode; detail: string; menu: ReactNode }) {
  return (
    <li className="flex items-center justify-between gap-4 py-2">
      <div className="min-w-0">
        {name}
        <p className="truncate text-sm text-muted-foreground">{detail}</p>
      </div>
      {menu}
    </li>
  );
}

function UploadRow({ upload, onCancel }: { upload: Upload; onCancel: () => void }) {
  const percent = upload.total > 0 ? Math.round((upload.loaded / upload.total) * 100) : 100;
  const inProgress = upload.status === "waiting" || upload.status === "uploading";
  const labels = {
    waiting: "Waiting",
    uploading: `${percent}%`,
    done: "Uploaded",
    failed: "Failed",
    cancelled: "Cancelled",
  };
  return (
    <li className="space-y-1 py-2">
      <div className="flex items-center justify-between gap-4 text-sm">
        <span className="truncate">{upload.name}</span>
        <span className="flex shrink-0 items-center gap-2 text-muted-foreground">
          {labels[upload.status]}
          {inProgress && (
            <Button variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
          )}
        </span>
      </div>
      {upload.status === "uploading" && (
        <progress
          value={percent}
          max={100}
          aria-label={`Uploading ${upload.name}`}
          className="h-1 w-full accent-primary"
        />
      )}
      {upload.status === "failed" && <FormError message={upload.error} />}
    </li>
  );
}

function SearchBox({ query }: { query: string | undefined }) {
  const navigate = useNavigate();
  const [text, setText] = useState(query ?? "");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const q = text.trim();
    void navigate({ to: "/", search: q ? { q } : {} });
  }

  return (
    <form role="search" onSubmit={handleSubmit}>
      <Input
        type="search"
        aria-label="Search your files"
        placeholder="Search your files"
        value={text}
        onChange={(event) => setText(event.target.value)}
      />
    </form>
  );
}

export function FilesPage({
  folderId,
  query,
}: {
  folderId: string | undefined;
  query: string | undefined;
}) {
  const navigate = useNavigate();
  const folder = useQuery({ ...folderQuery(folderId), enabled: !query });
  const search = useQuery({ ...searchQuery(query ?? ""), enabled: Boolean(query) });
  const { isPending, isError } = query ? search : folder;
  const data = query ? search.data && { path: [], ...search.data } : folder.data;
  const { data: connections } = useQuery(connectionsQuery);
  // Empty means the server picks the account with the most free space.
  const [chosenConnection, setChosenConnection] = useState("");
  const [action, setAction] = useState<Action | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const { uploads, add, cancel, clearFinished } = useUploads();
  const createFolder = useCreateFolder();
  const rename = useRename();
  const trash = useMoveToTrash();

  const hasConnections = (connections?.length ?? 0) > 0;
  const finished = uploads.some((u) => u.status !== "waiting" && u.status !== "uploading");
  const close = () => {
    setAction(null);
    rename.reset();
    createFolder.reset();
  };

  function handlePick(event: ChangeEvent<HTMLInputElement>) {
    const picked = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (picked.length > 0) {
      add(picked, { connectionId: chosenConnection || undefined, folderId });
    }
  }

  if (isError && !query) {
    return (
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">This folder isn't available</h1>
        <p className="text-muted-foreground">
          It may have been moved to the trash.{" "}
          <Link to="/" search={{}} className="underline underline-offset-4">
            Go to your files
          </Link>
        </p>
      </div>
    );
  }

  const empty = data && data.folders.length === 0 && data.files.length === 0;

  return (
    <div className="max-w-3xl space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        {query ? (
          <h1 className="min-w-0 truncate text-2xl font-semibold tracking-tight">Results for {query}</h1>
        ) : (
          <Breadcrumbs path={data?.path ?? []} />
        )}
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild variant="ghost">
            <Link to="/trash">Trash</Link>
          </Button>
          <Button variant="outline" onClick={() => setAction({ type: "new-folder" })}>
            New folder
          </Button>
          <input ref={picker} type="file" multiple hidden aria-label="Choose files" onChange={handlePick} />
          <Button disabled={!hasConnections} onClick={() => picker.current?.click()}>
            Upload
          </Button>
        </div>
      </div>

      <SearchBox key={query ?? ""} query={query} />

      {connections && !hasConnections && (
        <p className="text-sm text-muted-foreground">
          To upload files, first{" "}
          <Link to="/storage" className="text-foreground underline underline-offset-4">
            link a Google Drive account
          </Link>
          .
        </p>
      )}

      {connections && connections.length > 1 && (
        <label className="flex items-center gap-2 text-sm">
          <span className="shrink-0 text-muted-foreground">Upload to</span>
          <select
            className="h-9 min-w-0 rounded-md border border-input bg-background px-2"
            value={chosenConnection}
            onChange={(event) => setChosenConnection(event.target.value)}
          >
            <option value="">Account with the most free space</option>
            {connections.map((connection) => (
              <option key={connection.id} value={connection.id}>
                {connection.summary}
              </option>
            ))}
          </select>
        </label>
      )}

      {uploads.length > 0 && (
        <section aria-label="Uploads" className="space-y-1">
          <ul className="divide-y">
            {uploads.map((upload) => (
              <UploadRow key={upload.id} upload={upload} onCancel={() => cancel(upload.id)} />
            ))}
          </ul>
          {finished && (
            <Button variant="ghost" size="sm" onClick={clearFinished}>
              Clear finished
            </Button>
          )}
        </section>
      )}

      <FormError message={trash.error?.message} />
      {isPending && <p className="text-sm text-muted-foreground">Loading…</p>}
      {isError && query && <FormError message="The search didn't work. Try again." />}
      {empty && query && (
        <p className="text-sm text-muted-foreground">
          Nothing matches.{" "}
          <Link to="/" search={{}} className="text-foreground underline underline-offset-4">
            Go to your files
          </Link>
        </p>
      )}
      {empty && !query && hasConnections && (
        <p className="text-sm text-muted-foreground">This folder is empty. Upload a file or create a folder.</p>
      )}

      <ul className="divide-y">
        {data?.folders.map((folder) => {
          const item: Item = { kind: "folder", id: folder.id };
          return (
            <Row
              key={folder.id}
              name={
                <Link
                  to="/"
                  search={{ folder: folder.id }}
                  className="block truncate text-sm font-medium hover:underline"
                >
                  {folder.name}
                </Link>
              }
              detail="Folder"
              menu={
                <RowMenu label={folder.name}>
                  <DropdownMenuItem onSelect={() => navigate({ to: "/", search: { folder: folder.id } })}>
                    Open
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setAction({ type: "rename", item, name: folder.name })}>
                    Rename
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setAction({ type: "move", item, name: folder.name })}>
                    Move
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => trash.mutate(item)}>Move to trash</DropdownMenuItem>
                </RowMenu>
              }
            />
          );
        })}
        {data?.files.map((file) => {
          const item: Item = { kind: "file", id: file.id };
          const detail = `${formatBytes(file.size)}, ${formatDate(file.created_at)}`;
          return (
            <Row
              key={file.id}
              name={<FileName file={file} onPreview={() => setAction({ type: "preview", file })} />}
              detail={detail}
              menu={
                <RowMenu label={file.name}>
                  <DropdownMenuItem asChild>
                    <a href={contentUrl(file.id, false)} download>
                      Download
                    </a>
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setAction({ type: "rename", item, name: file.name })}>
                    Rename
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setAction({ type: "move", item, name: file.name })}>
                    Move
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => trash.mutate(item)}>Move to trash</DropdownMenuItem>
                </RowMenu>
              }
            />
          );
        })}
      </ul>

      {action?.type === "new-folder" && (
        <NameDialog
          title="New folder"
          label="Folder name"
          submitLabel="Create folder"
          initial=""
          pending={createFolder.isPending}
          error={createFolder.error?.message}
          onClose={close}
          onSubmit={(name) =>
            createFolder.mutate({ name, parent_id: folderId ?? null }, { onSuccess: close })
          }
        />
      )}
      {action?.type === "rename" && (
        <NameDialog
          title={`Rename ${action.name}`}
          label="New name"
          submitLabel="Rename"
          initial={action.name}
          pending={rename.isPending}
          error={rename.error?.message}
          onClose={close}
          onSubmit={(name) => rename.mutate({ item: action.item, name }, { onSuccess: close })}
        />
      )}
      {action?.type === "move" && <MoveDialog item={action.item} name={action.name} onClose={close} />}
      {action?.type === "preview" && <PreviewDialog file={action.file} onClose={close} />}
    </div>
  );
}

/**
 * Images and video open in a dialog, a PDF opens in its own tab, and anything
 * else downloads, because only those types are safe to show.
 */
function FileName({ file, onPreview }: { file: File; onPreview: () => void }) {
  const className = "block max-w-full truncate text-left text-sm font-medium hover:underline";
  if (file.preview === "image" || file.preview === "video") {
    return (
      <button type="button" className={className} onClick={onPreview}>
        {file.name}
      </button>
    );
  }
  if (file.preview === "pdf") {
    return (
      <a href={contentUrl(file.id, true)} target="_blank" rel="noopener noreferrer" className={className}>
        {file.name}
      </a>
    );
  }
  return (
    <a href={contentUrl(file.id, false)} download className={className}>
      {file.name}
    </a>
  );
}
