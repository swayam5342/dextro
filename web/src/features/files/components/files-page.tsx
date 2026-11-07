import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowLeftRight,
  CheckSquare,
  Download,
  Folder as FolderIcon,
  FolderInput,
  FolderOpen,
  FolderPlus,
  LayoutGrid,
  Link2,
  Lock,
  LockOpen,
  List,
  Pencil,
  RefreshCw,
  Star,
  StarOff,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { failureMessage } from "@/api/errors";
import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormError } from "@/components/field";
import { useToast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { syncText, usableConnections } from "@/features/storage/pool";
import { connectionsQuery, useSyncAccounts } from "@/features/storage/queries";
import { count, formatBytes, formatDate } from "@/lib/format";
import { recall, remember } from "@/lib/remember";
import { cn, keyIsTaken } from "@/lib/utils";

import {
  checkZip,
  contentUrl,
  folderQuery,
  itemKey,
  recentQuery,
  searchQuery,
  sharesQuery,
  starredQuery,
  useCreateFolder,
  useMoveToTrash,
  useRename,
  useSetFolderEncryption,
  useRestore,
  useSetStarred,
  zipUrl,
  type Item,
} from "../queries";
import { loadSort, saveSort, sorted, type SortId } from "../sort";
import { useDragMove } from "../use-drag-move";
import { AccountDialog, MoveDialog, NameDialog } from "./dialogs";
import {
  Blank,
  Breadcrumbs,
  FileTile,
  FolderTile,
  ListRow,
  ListThumb,
  SortHeading,
  SortMenu,
  type MenuEntry,
} from "./file-items";
import { SearchFilters } from "./search-filters";
import { ShareDialog } from "./share-dialog";
import { Viewer } from "./viewer";

type File = components["schemas"]["File"];
type Folder = components["schemas"]["Folder"];
type View = "grid" | "list";
type FileList = "recent" | "starred";

const FILE_LISTS = {
  recent: { title: "Recent", blank: "Nothing here yet", text: "The newest files in your pool show up here." },
  starred: { title: "Starred", blank: "No starred files", text: "Star a file from its menu to keep it here." },
};

type Action =
  | { type: "rename"; item: Item; name: string }
  | { type: "move"; items: Item[]; name: string }
  | { type: "account"; fileIds: string[]; name: string; from: string[] }
  | { type: "share"; fileId: string; name: string }
  | { type: "trash"; items: Item[]; name: string; viewed?: File }
  | { type: "encrypt"; folder: Folder }
  | { type: "new-folder" };

const VIEW_KEY = "dextro.view";

function loadView(): View {
  return recall(VIEW_KEY) === "list" ? "list" : "grid";
}

function save(url: string) {
  const link = document.createElement("a");
  link.href = url;
  link.download = "";
  link.click();
}

function download(file: File) {
  save(contentUrl(file.id, false));
}

function Browser({
  folderId,
  query,
  list,
}: {
  folderId: string | undefined;
  query: string | undefined;
  list: FileList | undefined;
}) {
  const navigate = useNavigate();
  /** A flat list of results has no folder of its own to add to or sync. */
  const flat = Boolean(query || list);
  const [sort, setSort] = useState<SortId>(() => (list ? "date-desc" : loadSort()));
  const folder = useQuery({ ...folderQuery(folderId), enabled: !flat });
  const search = useQuery({ ...searchQuery(query ?? "", sort), placeholderData: keepPreviousData, enabled: Boolean(query) });
  const recent = useQuery({ ...recentQuery, enabled: list === "recent" });
  const starred = useQuery({ ...starredQuery, enabled: list === "starred" });
  const listed = list === "recent" ? recent : starred;
  const { isPending, isError } = list ? listed : query ? search : folder;
  const results = query ? search.data : listed.data && { folders: [], ...listed.data };
  const data = flat ? results && { path: [], ...results } : folder.data;
  const { data: connections } = useQuery(connectionsQuery);
  const { data: links } = useQuery(sharesQuery);

  const [view, setView] = useState<View>(loadView);
  const [selected, setSelected] = useState<Item[]>([]);
  const [action, setAction] = useState<Action | null>(null);
  const [viewing, setViewing] = useState<string | null>(null);
  const createFolder = useCreateFolder();
  const rename = useRename();
  const encryption = useSetFolderEncryption();
  const setStarred = useSetStarred();
  const trash = useMoveToTrash();
  const restore = useRestore();
  const sync = useSyncAccounts();
  const toast = useToast();
  const queryClient = useQueryClient();

  const folders = useMemo(() => sorted(data?.folders ?? [], sort), [data?.folders, sort]);
  const files = useMemo(() => sorted(data?.files ?? [], sort), [data?.files, sort]);
  const path = data?.path ?? [];
  const selecting = selected.length > 0;
  const chosen = new Set(selected.map(itemKey));
  const viewIndex = files.findIndex((file) => file.id === viewing);
  const viewed = files[viewIndex];
  const accounts = new Map(connections?.map((c) => [c.id, c.summary]));
  const accountName = (id: string) => accounts.get(id) ?? "Unknown account";
  const shares = new Map(links?.shares.map((share) => [share.file_id, share]));
  const canChangeAccount = usableConnections(connections).length > 1;
  const { dragging, dropTarget, dragFrom, dropOn } = useDragMove({ selected, chosen, folders, files, path, deselect });

  useEffect(() => {
    if (!selecting) {
      return undefined;
    }
    function clearOnEscape(event: KeyboardEvent) {
      // Escape in a dialog or menu closes that, and leaves the selection alone.
      if (event.key === "Escape" && !keyIsTaken(event.target)) {
        setSelected([]);
      }
    }
    window.addEventListener("keydown", clearOnEscape);
    return () => window.removeEventListener("keydown", clearOnEscape);
  }, [selecting]);

  // Runs after every render, so the keys always act on what's on screen now.
  useEffect(() => {
    function handleKey(event: KeyboardEvent) {
      if (action || viewing || keyIsTaken(event.target)) {
        return;
      }
      if (event.key === "Delete" && selecting) {
        setAction({ type: "trash", items: selected, name: count(selected.length, "item") });
      } else if (event.key === "F2" && selected.length === 1 && selected[0]) {
        const item = selected[0];
        const named = [...folders, ...files].find((entry) => entry.id === item.id);
        if (named) {
          event.preventDefault();
          setAction({ type: "rename", item, name: named.name });
        }
      } else if (event.key === "a" && (event.ctrlKey || event.metaKey)) {
        event.preventDefault();
        setSelected([
          ...folders.map(({ id }): Item => ({ kind: "folder", id })),
          ...files.map(({ id }): Item => ({ kind: "file", id })),
        ]);
      }
    }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  });

  function toggle(item: Item) {
    setSelected((current) =>
      current.some((other) => itemKey(other) === itemKey(item))
        ? current.filter((other) => itemKey(other) !== itemKey(item))
        : [...current, item],
    );
  }

  function close() {
    setAction(null);
    rename.reset();
    createFolder.reset();
  }

  function handleSync() {
    sync.mutate(undefined, {
      onSuccess: (result) => toast({ message: syncText(result) }),
      onError: (error) => toast({ message: failureMessage(error) }),
    });
  }

  function changeView(next: View) {
    setView(next);
    remember(VIEW_KEY, next);
  }

  function changeSort(next: SortId) {
    setSort(next);
    // Recent and Starred start newest first, and a choice there stays there.
    if (!list) {
      saveSort(next);
    }
  }

  function deselect(items: Item[]) {
    const gone = new Set(items.map(itemKey));
    setSelected((current) => current.filter((item) => !gone.has(itemKey(item))));
  }

  async function undoTrash(items: Item[]) {
    try {
      for (const item of items) {
        await restore.mutateAsync(item);
      }
      toast({ message: `Restored ${count(items.length, "item")}` });
    } catch (error) {
      toast({ message: failureMessage(error) });
    }
  }

  function linkCount(): number {
    return queryClient.getQueryData(sharesQuery.queryKey)?.shares.length ?? 0;
  }

  /** A link is gone for good, and Undo won't bring it back, so this has to say. */
  function announceTrash(items: Item[], linksBefore: number) {
    const ended = linksBefore - linkCount();
    let links = "";
    if (ended > 0) {
      links = ended === 1 ? " and removed its Google link" : ` and removed ${ended} Google links`;
    }
    toast({
      message: `Moved ${count(items.length, "item")} to the trash${links}`,
      action: { label: "Undo", run: () => void undoTrash(items) },
    });
  }

  /** One at a time, so a failure stops here and its message stays on screen. */
  async function moveToTrash(items: Item[]) {
    trash.reset();
    const linksBefore = linkCount();
    const done: Item[] = [];
    try {
      for (const item of items) {
        await trash.mutateAsync(item);
        done.push(item);
      }
    } catch {
      // The mutation's error is shown on the page.
    }
    deselect(done);
    if (done.length > 0) {
      announceTrash(done, linksBefore);
    }
  }

  /** What going to the trash means, including what Undo can't bring back. */
  function trashText(items: Item[]): string {
    const many = items.length > 1;
    const parts = [`You can restore ${many ? "them" : "it"} from the trash.`];
    if (items.some((item) => item.kind === "folder")) {
      parts.push("Everything inside goes too.");
      if (shares.size > 0) {
        parts.push("Any Google links inside will end, and restoring won't bring them back.");
      }
    } else if (items.some((item) => shares.has(item.id))) {
      parts.push(`${many ? "Their Google links" : "Its Google link"} will end, and restoring won't bring ${many ? "them" : "it"} back.`);
    }
    return parts.join(" ");
  }

  function askViewerTrash(file: File) {
    setAction({ type: "trash", items: [{ kind: "file", id: file.id }], name: file.name, viewed: file });
  }

  function handleViewerTrash(file: File) {
    const item: Item = { kind: "file", id: file.id };
    const next = files[viewIndex + 1] ?? files[viewIndex - 1];
    const linksBefore = linkCount();
    trash.mutate(item, {
      onSuccess: () => {
        setViewing(next?.id ?? null);
        announceTrash([item], linksBefore);
      },
    });
  }

  async function downloadItems(items: Item[]) {
    try {
      await checkZip(items);
      save(zipUrl(items));
    } catch (error) {
      toast({ message: failureMessage(error) });
    }
  }

  function downloadSelected() {
    const [first] = selected;
    if (selected.length === 1 && first?.kind === "file") {
      save(contentUrl(first.id, false));
    } else {
      void downloadItems(selected);
    }
  }

  /** What a folder needs in either view. */
  function folderProps(target: Folder) {
    const item: Item = { kind: "folder", id: target.id };
    return {
      encrypted: target.encrypted,
      selected: chosen.has(itemKey(item)),
      selecting,
      entries: folderEntries(target),
      drag: { ...dragFrom(item), ...dropOn(target.id) },
      lit: dropTarget === target.id,
      onToggle: () => toggle(item),
    };
  }

  /** What a file needs in either view. */
  function fileProps(file: File) {
    const item: Item = { kind: "file", id: file.id };
    return {
      selected: chosen.has(itemKey(item)),
      selecting,
      entries: fileEntries(file),
      drag: dragFrom(item),
      shared: shares.has(file.id),
      starred: file.starred,
      encrypted: file.encrypted,
      onToggle: () => toggle(item),
    };
  }

  function folderEntries(target: Folder): MenuEntry[] {
    const item: Item = { kind: "folder", id: target.id };
    return [
      { label: "Open", icon: FolderOpen, run: () => void navigate({ to: "/", search: { folder: target.id } }) },
      { label: "Download as zip", icon: Download, run: () => void downloadItems([item]) },
      { label: "Select", icon: CheckSquare, run: () => toggle(item) },
      { label: "Rename", icon: Pencil, run: () => setAction({ type: "rename", item, name: target.name }) },
      { label: "Move", icon: FolderInput, run: () => setAction({ type: "move", items: [item], name: target.name }) },
      {
        label: target.encrypted ? "Stop encrypting new files" : "Encrypt new files",
        icon: target.encrypted ? LockOpen : Lock,
        run: () => setAction({ type: "encrypt", folder: target }),
      },
      { label: "Move to trash", icon: Trash2, run: () => setAction({ type: "trash", items: [item], name: target.name }) },
    ];
  }

  function fileEntries(file: File): MenuEntry[] {
    const item: Item = { kind: "file", id: file.id };
    const shared = shares.has(file.id);
    const linkEntry: MenuEntry = {
      label: shared ? "Manage Google link" : "Get Google link",
      icon: Link2,
      run: () => setAction({ type: "share", fileId: file.id, name: file.name }),
    };
    const toAccount: MenuEntry = {
      label: "Move to another account",
      icon: ArrowLeftRight,
      run: () => setAction({ type: "account", fileIds: [file.id], name: file.name, from: [file.connection_id] }),
    };
    const showInFolder: MenuEntry = {
      label: "Show in folder",
      icon: FolderOpen,
      run: () => void navigate({ to: "/", search: file.folder_id ? { folder: file.folder_id } : {} }),
    };
    return [
      { label: "Download", icon: Download, run: () => download(file) },
      ...(flat ? [showInFolder] : []),
      {
        label: file.starred ? "Remove star" : "Star",
        icon: file.starred ? StarOff : Star,
        run: () =>
          setStarred.mutate(
            { id: file.id, starred: !file.starred },
            { onError: (error) => toast({ message: failureMessage(error) }) },
          ),
      },
      { label: "Select", icon: CheckSquare, run: () => toggle(item) },
      { label: "Rename", icon: Pencil, run: () => setAction({ type: "rename", item, name: file.name }) },
      { label: "Move", icon: FolderInput, run: () => setAction({ type: "move", items: [item], name: file.name }) },
      // An added file is the person's own, and stays in the account it came from.
      ...(canChangeAccount && !file.external && !shared ? [toAccount] : []),
      ...((links?.enabled || shared) && !file.external && !file.encrypted ? [linkEntry] : []),
      { label: "Move to trash", icon: Trash2, run: () => setAction({ type: "trash", items: [item], name: file.name }) },
    ];
  }

  if (isError && !flat) {
    return (
      <Blank title="This folder isn't available" text="It may have been moved to the trash.">
        <Button asChild variant="outline">
          <Link to="/" search={{}}>
            Go to your files
          </Link>
        </Button>
      </Blank>
    );
  }

  const empty = data && folders.length === 0 && files.length === 0;
  const noAccounts = connections?.length === 0;
  const chosenFiles = files.filter((file) => chosen.has(itemKey({ kind: "file", id: file.id })));
  const canMoveSelection =
    canChangeAccount &&
    chosenFiles.length === selected.length &&
    !chosenFiles.some((file) => file.external || shares.has(file.id));

  return (
    <div>
      {selecting ? (
        <div className="sticky -top-4 z-10 -mx-4 -mt-4 mb-[18px] flex min-h-[52px] items-center gap-1 border-b bg-background px-4 md:-top-[22px] md:-mx-7 md:-mt-[22px] md:min-h-[58px] md:px-7">
          <Button variant="ghost" size="icon" aria-label="Clear selection" onClick={() => setSelected([])}>
            <X />
          </Button>
          {/* A drag needs somewhere to go, and the path is hidden while selecting. */}
          {dragging && path.length > 0 ? (
            <Breadcrumbs path={path} dropOn={dropOn} dropTarget={dropTarget} />
          ) : (
            <span className="mr-auto font-semibold">{selected.length} selected</span>
          )}
          <Button variant="ghost" aria-label="Download selected items" onClick={downloadSelected}>
            <Download />
            <span className="hidden sm:inline">Download</span>
          </Button>
          <Button
            variant="ghost"
            aria-label="Move selected items"
            onClick={() => setAction({ type: "move", items: selected, name: count(selected.length, "item") })}
          >
            <FolderInput />
            <span className="hidden sm:inline">Move</span>
          </Button>
          {canMoveSelection && (
            <Button
              variant="ghost"
              aria-label="Move selected files to another account"
              onClick={() =>
                setAction({
                  type: "account",
                  fileIds: chosenFiles.map(({ id }) => id),
                  name: count(chosenFiles.length, "file"),
                  from: [...new Set(chosenFiles.map((file) => file.connection_id))],
                })
              }
            >
              <ArrowLeftRight />
              <span className="hidden sm:inline">Account</span>
            </Button>
          )}
          <Button variant="ghost" aria-label="Move selected items to the trash" onClick={() => setAction({ type: "trash", items: selected, name: count(selected.length, "item") })}>
            <Trash2 />
            <span className="hidden sm:inline">Trash</span>
          </Button>
        </div>
      ) : (
        <div className="mb-[18px] flex min-h-9 items-center gap-2">
          {flat ? (
            <h1 className="min-w-0 flex-1 truncate text-2xl font-semibold tracking-tight">
              {list ? FILE_LISTS[list].title : `Results for ${query}`}
            </h1>
          ) : (
            <Breadcrumbs path={path} dropOn={dropOn} dropTarget={dropTarget} />
          )}
          {!flat && (
            <Button variant="outline" aria-label="New folder" onClick={() => setAction({ type: "new-folder" })}>
              <FolderPlus />
              <span className="hidden sm:inline">New folder</span>
            </Button>
          )}
          {!flat && !noAccounts && (
            <Button variant="outline" aria-label="Sync with Drive" disabled={sync.isPending} onClick={handleSync}>
              <RefreshCw className={cn(sync.isPending && "motion-safe:animate-spin")} />
              <span className="hidden sm:inline">Sync</span>
            </Button>
          )}
          <SortMenu sort={sort} onSort={changeSort} />
          <div role="group" aria-label="View" className="flex rounded-full bg-card p-0.5">
            {(
              [
                ["grid", LayoutGrid, "Grid view"],
                ["list", List, "List view"],
              ] as const
            ).map(([value, Icon, label]) => (
              <button
                key={value}
                type="button"
                aria-label={label}
                aria-pressed={view === value}
                onClick={() => changeView(value)}
                className={cn(
                  "grid size-8 place-items-center rounded-full text-muted-foreground",
                  view === value && "bg-raised text-foreground",
                )}
              >
                <Icon className="size-4" />
              </button>
            ))}
          </div>
        </div>
      )}
      {query && <SearchFilters query={query} />}
      {!flat && folder.data?.encrypted && (
        <p className="-mt-2.5 mb-[18px] flex items-center gap-1.5 text-[13px] text-muted-foreground">
          <Lock className="size-3.5" />
          New files here are encrypted.
        </p>
      )}

      {!selecting && !query && !folderId && (
        <div className="mb-[18px] flex gap-2 md:hidden">
          {(["recent", "starred"] as const).map((name) => (
            <Button key={name} asChild variant="outline" size="sm">
              <Link to={`/${name}`}>{FILE_LISTS[name].title}</Link>
            </Button>
          ))}
        </div>
      )}
      <FormError message={viewing ? undefined : trash.error?.message} />
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && query && <FormError message="The search didn't work. Try again." />}
      {isError && list && <FormError message="This list couldn't be loaded. Try again." />}

      {empty && query && <Blank title="Nothing matches" text="Try part of the name, or a different word." />}
      {empty && list && <Blank title={FILE_LISTS[list].blank} text={FILE_LISTS[list].text} />}
      {empty && !flat && noAccounts && (
        <Blank title="Link an account to start" text="Your files live in your own Google Drive accounts.">
          <Button asChild>
            <Link to="/storage">Link a Google Drive account</Link>
          </Button>
        </Blank>
      )}
      {empty && !flat && !noAccounts && (
        <Blank title="This folder is empty" text="Drop files anywhere on this page, or use Upload." />
      )}

      {view === "grid" && !empty && (
        <>
          {folders.length > 0 && (
            <ul className="mb-[22px] grid grid-cols-2 gap-3.5 md:grid-cols-[repeat(auto-fill,minmax(190px,1fr))]">
              {folders.map((target) => (
                <FolderTile key={target.id} value={target} {...folderProps(target)} />
              ))}
            </ul>
          )}
          <ul className="mosaic">
            {files.map((file) => (
              <FileTile key={file.id} value={file} {...fileProps(file)} onOpen={() => setViewing(file.id)} />
            ))}
          </ul>
        </>
      )}

      {view === "list" && !empty && data && (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="text-left text-xs text-faint">
              <SortHeading label="Name" field="name" sort={sort} onSort={changeSort} />
              <SortHeading label="Size" field="size" sort={sort} onSort={changeSort} className="text-right" />
              <th scope="col" className="hidden px-3 pb-2 font-normal lg:table-cell">Stored in</th>
              <SortHeading label="Added" field="date" sort={sort} onSort={changeSort} className="hidden sm:table-cell" />
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {folders.map((target) => (
              <ListRow
                key={target.id}
                name={target.name}
                thumb={
                  <span className="grid size-[38px] shrink-0 place-items-center rounded-[7px] bg-card">
                    <FolderIcon className="size-[18px] fill-raised text-foreground" />
                  </span>
                }
                open={
                  <Link to="/" search={{ folder: target.id }} draggable={false} className="truncate hover:underline">
                    {target.name}
                  </Link>
                }
                size=""
                account=""
                date={formatDate(target.created_at)}
                {...folderProps(target)}
              />
            ))}
            {files.map((file) => (
              <ListRow
                key={file.id}
                name={file.name}
                thumb={<ListThumb file={file} />}
                open={
                  <button type="button" className="truncate text-left hover:underline" onClick={() => setViewing(file.id)}>
                    {file.name}
                  </button>
                }
                size={formatBytes(file.size)}
                account={accountName(file.connection_id)}
                date={formatDate(file.created_at)}
                {...fileProps(file)}
              />
            ))}
          </tbody>
        </table>
      )}

      {viewed && (
        <Viewer
          file={viewed}
          previous={files[viewIndex - 1]}
          next={files[viewIndex + 1]}
          accountName={accountName}
          trashError={trash.error?.message}
          onShow={(file) => setViewing(file.id)}
          onTrash={askViewerTrash}
          onClose={() => {
            setViewing(null);
            trash.reset();
          }}
        />
      )}
      {action?.type === "new-folder" && (
        <NameDialog
          title="New folder"
          label="Folder name"
          submitLabel="Create folder"
          initial=""
          pending={createFolder.isPending}
          error={createFolder.error?.message}
          onClose={close}
          onSubmit={(name) => createFolder.mutate({ name, parent_id: folderId ?? null }, { onSuccess: close })}
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
      {action?.type === "trash" && (
        <ConfirmDialog
          title={`Move ${action.name} to the trash?`}
          text={trashText(action.items)}
          action="Move to trash"
          onClose={close}
          onConfirm={() => {
            close();
            if (action.viewed) {
              handleViewerTrash(action.viewed);
            } else {
              void moveToTrash(action.items);
            }
          }}
        />
      )}
      {action?.type === "encrypt" && (
        <ConfirmDialog
          title={action.folder.encrypted ? `Stop encrypting new files in ${action.folder.name}?` : `Encrypt new files in ${action.folder.name}?`}
          text={
            action.folder.encrypted
              ? "New files here will go to Google as they are. Files already encrypted stay encrypted and keep working."
              : "New files here and in the folders inside will be encrypted before they reach Google, so only this server can read them. Their names stay visible in Drive. Files already here stay as they are. Encrypted files have no thumbnails or Google links, and if this server's ENCRYPTION_KEYS is ever lost, they can't be recovered."
          }
          action={action.folder.encrypted ? "Stop encrypting" : "Encrypt new files"}
          safe
          pending={encryption.isPending}
          error={encryption.error?.message}
          onClose={close}
          onConfirm={() => encryption.mutate({ id: action.folder.id, encrypted: !action.folder.encrypted }, { onSuccess: close })}
        />
      )}
      {action?.type === "share" && <ShareDialog fileId={action.fileId} name={action.name} onClose={close} />}
      {action?.type === "account" && (
        <AccountDialog
          fileIds={action.fileIds}
          name={action.name}
          from={action.from}
          onClose={close}
          onDone={(queued, account) => {
            close();
            setSelected([]);
            toast({
              message:
                queued === 0
                  ? `Already in ${account}`
                  : `Moving ${count(queued, "file")} to ${account}. Follow it on the Storage page.`,
            });
          }}
        />
      )}
      {action?.type === "move" && (
        <MoveDialog
          items={action.items}
          name={action.name}
          onClose={close}
          onDone={() => {
            close();
            setSelected([]);
          }}
        />
      )}
    </div>
  );
}

/** Keyed by what's on screen, so a selection doesn't carry into another folder. */
export function FilesPage({
  folderId,
  query,
  list,
}: {
  folderId: string | undefined;
  query: string | undefined;
  list?: FileList;
}) {
  return <Browser key={`${folderId ?? "top"}:${query ?? ""}:${list ?? ""}`} folderId={folderId} query={query} list={list} />;
}
