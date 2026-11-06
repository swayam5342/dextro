import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowDown,
  ArrowLeftRight,
  ArrowUp,
  ArrowUpDown,
  Check,
  CheckSquare,
  Download,
  Folder as FolderIcon,
  FolderInput,
  FolderPlus,
  FolderOpen,
  LayoutGrid,
  Link2,
  List,
  MoreVertical,
  Pencil,
  Play,
  Trash2,
  X,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useState, type HTMLAttributes, type MouseEvent } from "react";

import { failureMessage } from "@/api/errors";
import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { useToast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { connectionsQuery } from "@/features/storage/queries";
import { formatBytes, formatDate } from "@/lib/format";
import { cn } from "@/lib/utils";

import {
  checkZip,
  contentUrl,
  folderQuery,
  searchQuery,
  sharesQuery,
  thumbnailUrl,
  useCreateFolder,
  useMove,
  useMoveToTrash,
  useRename,
  useRestore,
  zipUrl,
  type Item,
} from "../queries";
import { loadSort, nextSort, saveSort, sorted, SORTS, type SortField, type SortId } from "../sort";
import { AccountDialog, MoveDialog, NameDialog } from "./dialogs";
import { fileIcon } from "./file-icon";
import { ShareDialog } from "./share-dialog";
import { Viewer } from "./viewer";

type File = components["schemas"]["File"];
type Folder = components["schemas"]["Folder"];
type View = "grid" | "list";

type Action =
  | { type: "rename"; item: Item; name: string }
  | { type: "move"; items: Item[]; name: string }
  | { type: "account"; fileIds: string[]; name: string; from: string[] }
  | { type: "share"; fileId: string; name: string }
  | { type: "new-folder" };

/** Something being dragged, and the folder to put it back in. */
interface Moving {
  item: Item;
  from: string | null;
}

interface MenuEntry {
  label: string;
  icon: LucideIcon;
  run: () => void;
}

const VIEW_KEY = "dextro.view";
const SHOW_ON_TOUCH = "[@media(hover:none)]:opacity-100";
/** Has no "Files" type, so the upload drop zone leaves these drags alone. */
const DRAG_TYPE = "application/x-dextro-items";
const TOP_LEVEL = "top";

/** A remembered convenience, so it does without storage when the browser refuses it. */
function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === "list" ? "list" : "grid";
  } catch {
    return "grid";
  }
}

function saveView(view: View) {
  try {
    localStorage.setItem(VIEW_KEY, view);
  } catch {
    // It just won't be remembered.
  }
}

function itemKey(item: Item): string {
  return `${item.kind}:${item.id}`;
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

function countOf(items: unknown[]): string {
  return items.length === 1 ? "1 item" : `${items.length} items`;
}

/** Shows how many are moving, because a browser only pictures the one that was grabbed. */
function setCountPicture(transfer: DataTransfer, count: number) {
  const picture = document.createElement("div");
  picture.textContent = `${count} items`;
  picture.className = "fixed -top-24 left-0 rounded-full bg-raised px-3 py-1.5 text-sm font-medium text-foreground";
  document.body.append(picture);
  transfer.setDragImage(picture, 0, 0);
  setTimeout(() => picture.remove());
}

function Breadcrumbs({
  path,
  dropOn,
  dropTarget,
}: {
  path: Folder[];
  dropOn: (target: string | null) => HTMLAttributes<HTMLElement>;
  dropTarget: string | null;
}) {
  const title = "text-xl font-semibold tracking-tight";
  const lit = "rounded-md text-foreground outline-2 outline-offset-2 outline-white";
  return (
    <nav aria-label="Folders" className={cn("flex min-w-0 flex-1 items-center gap-x-2", title)}>
      {path.length === 0 ? (
        <h1>Files</h1>
      ) : (
        <Link
          to="/"
          search={{}}
          draggable={false}
          {...dropOn(null)}
          className={cn("text-muted-foreground hover:text-foreground", dropTarget === TOP_LEVEL && lit)}
        >
          Files
        </Link>
      )}
      {path.map((folder, index) => (
        <span key={folder.id} className="flex min-w-0 items-center gap-2">
          <span className="text-faint">/</span>
          {index === path.length - 1 ? (
            <h1 className={cn("truncate", title)}>{folder.name}</h1>
          ) : (
            <Link
              to="/"
              search={{ folder: folder.id }}
              draggable={false}
              {...dropOn(folder.id)}
              className={cn("truncate text-muted-foreground hover:text-foreground", dropTarget === folder.id && lit)}
            >
              {folder.name}
            </Link>
          )}
        </span>
      ))}
    </nav>
  );
}

function SortMenu({ sort, onSort }: { sort: SortId; onSort: (sort: SortId) => void }) {
  const current = SORTS.find((option) => option.id === sort)?.label;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" aria-label={`Sort: ${current}`}>
          <ArrowUpDown />
          <span className="hidden lg:inline">{current}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {SORTS.map((option) => (
          <DropdownMenuItem key={option.id} onSelect={() => onSort(option.id)}>
            <span className="flex-1">{option.label}</span>
            {option.id === sort && <Check />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SortHeading({
  label,
  field,
  sort,
  onSort,
  className,
}: {
  label: string;
  field: SortField;
  sort: SortId;
  onSort: (sort: SortId) => void;
  className?: string;
}) {
  const active = sort.startsWith(field);
  const ascending = sort.endsWith("asc");
  const Arrow = ascending ? ArrowUp : ArrowDown;
  let order: "ascending" | "descending" | undefined;
  if (active) {
    order = ascending ? "ascending" : "descending";
  }
  return (
    <th scope="col" aria-sort={order} className={cn("px-3 pb-2 font-normal", className)}>
      <button
        type="button"
        className={cn("inline-flex items-center gap-1 hover:text-foreground", active && "text-foreground")}
        onClick={() => onSort(nextSort(sort, field))}
      >
        {label}
        {active && <Arrow className="size-3" />}
      </button>
    </th>
  );
}

/** Marks a file anyone with a link can open, so public files can't go unnoticed. */
function LinkBadge({ className }: { className?: string }) {
  return (
    <span role="img" aria-label="Has a Google link" title="Has a Google link" className={cn("grid size-6 shrink-0 place-items-center rounded-full", className)}>
      <Link2 className="size-3.5" />
    </span>
  );
}

function RightClickMenu({ entries }: { entries: MenuEntry[] }) {
  return (
    <ContextMenuContent>
      {entries.map(({ label, icon: Icon, run }) => (
        <ContextMenuItem key={label} onSelect={run}>
          <Icon />
          {label}
        </ContextMenuItem>
      ))}
    </ContextMenuContent>
  );
}

function DotsMenu({ label, entries, className }: { label: string; entries: MenuEntry[]; className?: string }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Actions for ${label}`}
        className={cn("grid size-[30px] shrink-0 place-items-center rounded-full text-white", className)}
      >
        <MoreVertical className="size-[18px]" />
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {entries.map(({ label: entry, icon: Icon, run }) => (
          <DropdownMenuItem key={entry} onSelect={run}>
            <Icon />
            {entry}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function PickButton({
  name,
  selected,
  visible,
  onToggle,
  className,
}: {
  name: string;
  selected: boolean;
  visible: boolean;
  onToggle: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={selected}
      aria-label={`Select ${name}`}
      onClick={onToggle}
      className={cn(
        "grid size-6 shrink-0 place-items-center rounded-full border-[1.5px] border-white bg-black/55 text-white opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100",
        (visible || selected) && "opacity-100",
        selected && "bg-white text-black",
        className,
      )}
    >
      {selected && <Check className="size-3.5" strokeWidth={2.4} />}
    </button>
  );
}

interface TileProps<T> {
  value: T;
  selected: boolean;
  selecting: boolean;
  entries: MenuEntry[];
  drag: HTMLAttributes<HTMLElement>;
  onToggle: () => void;
}

function FolderTile({
  value: folder,
  selected,
  selecting,
  entries,
  drag,
  lit,
  onToggle,
}: TileProps<Folder> & { lit: boolean }) {
  function handleClick(event: MouseEvent) {
    if (selecting || event.ctrlKey || event.metaKey) {
      event.preventDefault();
      onToggle();
    }
  }

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <li
          {...drag}
          className={cn(
            "group relative flex min-w-0 items-center gap-1 rounded-[14px] bg-card pr-2 transition-colors hover:bg-accent",
            selected && "outline-2 outline-white",
            lit && "bg-raised outline-2 outline-white",
          )}
        >
          <Link
            to="/"
            search={{ folder: folder.id }}
            draggable={false}
            onClick={handleClick}
            className="flex min-w-0 flex-1 items-center gap-3 py-3 pl-3.5"
          >
            <FolderIcon className="size-[22px] shrink-0 text-muted-foreground" />
            <span className="min-w-0 leading-tight">
              <span className="block truncate font-medium">{folder.name}</span>
              <span className="hidden truncate text-xs text-faint md:block">{formatDate(folder.created_at)}</span>
            </span>
          </Link>
          <PickButton name={folder.name} selected={selected} visible={selecting} onToggle={onToggle} />
          <DotsMenu
            label={folder.name}
            entries={entries}
            className={cn(
              "opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100 hover:bg-raised",
              SHOW_ON_TOUCH,
            )}
          />
        </li>
      </ContextMenuTrigger>
      <RightClickMenu entries={entries} />
    </ContextMenu>
  );
}

function FileTile({
  value: file,
  selected,
  selecting,
  entries,
  drag,
  shared,
  onToggle,
  onOpen,
}: TileProps<File> & { shared: boolean; onOpen: () => void }) {
  const [failed, setFailed] = useState(false);
  const [ratio, setRatio] = useState(1.3);
  const hasPicture = file.preview !== "none" && !failed;
  const Icon = fileIcon(file.content_type);

  function handleClick(event: MouseEvent) {
    if (selecting || event.ctrlKey || event.metaKey) {
      onToggle();
      return;
    }
    onOpen();
  }

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <li
          {...drag}
          className={cn(
            "group relative h-(--row) min-w-0 overflow-hidden rounded-[10px] bg-card",
            selected && "outline-2 outline-white",
          )}
          style={{ flex: `${ratio} 1 calc(var(--row) * ${ratio})` }}
        >
          <button type="button" aria-label={`Open ${file.name}`} onClick={handleClick} className="block size-full text-left">
            {hasPicture ? (
              <img
                src={thumbnailUrl(file.id)}
                alt=""
                loading="lazy"
                draggable={false}
                className="size-full object-cover transition-transform duration-200 group-hover:scale-[1.025]"
                onError={() => setFailed(true)}
                onLoad={(event) => {
                  const { naturalWidth, naturalHeight } = event.currentTarget;
                  // Limits keep a panorama from filling a row, or a tall scan from vanishing.
                  setRatio(Math.min(Math.max(naturalWidth / naturalHeight, 0.6), 2.2));
                }}
              />
            ) : (
              <span className="flex h-full flex-col justify-end gap-1.5 p-3.5 md:gap-2.5">
                <Icon className="size-[22px] text-muted-foreground md:size-[30px]" />
                <span className="min-w-0">
                  <span className="line-clamp-1 leading-tight font-medium break-words md:line-clamp-2">{file.name}</span>
                  <span className="mt-0.5 block text-xs text-faint">{formatBytes(file.size)}</span>
                </span>
              </span>
            )}
          </button>
          {hasPicture && file.preview === "video" && (
            <span className="pointer-events-none absolute top-1/2 left-1/2 grid size-[46px] -translate-1/2 place-items-center rounded-full bg-black/55">
              <Play className="ml-0.5 size-5 fill-white stroke-none" />
            </span>
          )}
          {hasPicture && (
            <span className="pointer-events-none absolute inset-x-0 bottom-0 truncate bg-linear-to-t from-black/75 to-transparent px-2.5 pt-6.5 pb-2 text-[12.5px] opacity-0 transition-opacity group-hover:opacity-100">
              {file.name}
            </span>
          )}
          {shared && <LinkBadge className="pointer-events-none absolute right-2 bottom-2 bg-black/55 text-white" />}
          <PickButton
            name={file.name}
            selected={selected}
            visible={selecting}
            onToggle={onToggle}
            className="absolute top-2 left-2"
          />
          <DotsMenu
            label={file.name}
            entries={entries}
            className={cn(
              "absolute top-1.5 right-1.5 bg-black/55 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
              SHOW_ON_TOUCH,
            )}
          />
        </li>
      </ContextMenuTrigger>
      <RightClickMenu entries={entries} />
    </ContextMenu>
  );
}

function ListThumb({ file }: { file: File }) {
  const [failed, setFailed] = useState(false);
  const Icon = fileIcon(file.content_type);
  return (
    <span className="grid size-[38px] shrink-0 place-items-center overflow-hidden rounded-[7px] bg-card">
      {file.preview !== "none" && !failed ? (
        <img
          src={thumbnailUrl(file.id)}
          alt=""
          loading="lazy"
          draggable={false}
          className="size-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : (
        <Icon className="size-[18px] text-muted-foreground" />
      )}
    </span>
  );
}

interface ListRowProps {
  name: string;
  thumb: React.ReactNode;
  open: React.ReactNode;
  size: string;
  account: string;
  date: string;
  selected: boolean;
  selecting: boolean;
  entries: MenuEntry[];
  drag: HTMLAttributes<HTMLElement>;
  lit?: boolean;
  shared?: boolean;
  onToggle: () => void;
}

function ListRow({
  name,
  thumb,
  open,
  size,
  account,
  date,
  selected,
  selecting,
  entries,
  drag,
  lit,
  shared,
  onToggle,
}: ListRowProps) {
  const cell = "border-t px-3 py-2 whitespace-nowrap text-muted-foreground group-hover:bg-[#0c0c0c]";
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <tr {...drag} className={cn("group", selected && "[&>td]:bg-accent", lit && "[&>td]:bg-raised")}>
          <td className={cn(cell, "w-full max-w-0 text-foreground")}>
            <span className="flex min-w-0 items-center gap-3">
              <span className="relative shrink-0">
                {thumb}
                <PickButton
                  name={name}
                  selected={selected}
                  visible={selecting}
                  onToggle={onToggle}
                  className="absolute top-[7px] left-[7px]"
                />
              </span>
              {open}
              {shared && <LinkBadge className="text-muted-foreground" />}
            </span>
          </td>
          <td className={cn(cell, "text-right")}>{size}</td>
          <td className={cn(cell, "hidden max-w-56 truncate lg:table-cell")}>{account}</td>
          <td className={cn(cell, "hidden sm:table-cell")}>{date}</td>
          <td className={cn(cell, "px-1")}>
            <DotsMenu label={name} entries={entries} className="text-muted-foreground hover:bg-raised hover:text-foreground" />
          </td>
        </tr>
      </ContextMenuTrigger>
      <RightClickMenu entries={entries} />
    </ContextMenu>
  );
}

function Blank({ title, text, children }: { title: string; text: string; children?: React.ReactNode }) {
  return (
    <div className="grid justify-items-start gap-1 py-10">
      <h2 className="text-base font-semibold">{title}</h2>
      <p className="text-muted-foreground">{text}</p>
      {children && <div className="mt-3">{children}</div>}
    </div>
  );
}

function Browser({ folderId, query }: { folderId: string | undefined; query: string | undefined }) {
  const navigate = useNavigate();
  const folder = useQuery({ ...folderQuery(folderId), enabled: !query });
  const search = useQuery({ ...searchQuery(query ?? ""), enabled: Boolean(query) });
  const { isPending, isError } = query ? search : folder;
  const data = query ? search.data && { path: [], ...search.data } : folder.data;
  const { data: connections } = useQuery(connectionsQuery);
  const { data: links } = useQuery(sharesQuery);

  const [view, setView] = useState<View>(loadView);
  const [sort, setSort] = useState<SortId>(loadSort);
  const [selected, setSelected] = useState<Item[]>([]);
  const [dragging, setDragging] = useState<Moving[] | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [action, setAction] = useState<Action | null>(null);
  const [viewing, setViewing] = useState<string | null>(null);
  const createFolder = useCreateFolder();
  const rename = useRename();
  const trash = useMoveToTrash();
  const restore = useRestore();
  const move = useMove();
  const toast = useToast();

  const folders = useMemo(() => sorted(data?.folders ?? [], sort), [data?.folders, sort]);
  const files = useMemo(() => sorted(data?.files ?? [], sort), [data?.files, sort]);
  const selecting = selected.length > 0;
  const chosen = new Set(selected.map(itemKey));
  const viewIndex = files.findIndex((file) => file.id === viewing);
  const viewed = files[viewIndex];
  const accounts = new Map(connections?.map((c) => [c.id, c.summary]));
  const accountName = (id: string) => accounts.get(id) ?? "Unknown account";
  const shares = new Map(links?.shares.map((share) => [share.file_id, share]));
  const canChangeAccount = (connections?.filter((connection) => !connection.draining).length ?? 0) > 1;

  useEffect(() => {
    if (!selecting) {
      return undefined;
    }
    function clearOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setSelected([]);
      }
    }
    window.addEventListener("keydown", clearOnEscape);
    return () => window.removeEventListener("keydown", clearOnEscape);
  }, [selecting]);

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

  function changeView(next: View) {
    setView(next);
    saveView(next);
  }

  function changeSort(next: SortId) {
    setSort(next);
    saveSort(next);
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
      toast({ message: `Restored ${countOf(items)}` });
    } catch (error) {
      toast({ message: failureMessage(error) });
    }
  }

  function announceTrash(items: Item[]) {
    // The link is gone for good, and Undo won't bring it back, so this has to say.
    const hadLink = items.some((item) => item.kind === "file" && shares.has(item.id));
    toast({
      message: `Moved ${countOf(items)} to the trash${hadLink ? " and removed its Google link" : ""}`,
      action: { label: "Undo", run: () => void undoTrash(items) },
    });
  }

  /** One at a time, so a failure stops here and its message stays on screen. */
  async function moveToTrash(items: Item[]) {
    trash.reset();
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
      announceTrash(done);
    }
  }

  function handleViewerTrash(file: File) {
    const item: Item = { kind: "file", id: file.id };
    const next = files[viewIndex + 1] ?? files[viewIndex - 1];
    trash.mutate(item, {
      onSuccess: () => {
        setViewing(next?.id ?? null);
        announceTrash([item]);
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
    const file = selected.length === 1 && first?.kind === "file" ? files.find(({ id }) => id === first.id) : undefined;
    if (file) {
      download(file);
    } else {
      void downloadItems(selected);
    }
  }

  function originOf(item: Item): string | null {
    if (item.kind === "file") {
      return files.find(({ id }) => id === item.id)?.folder_id ?? null;
    }
    return folders.find(({ id }) => id === item.id)?.parent_id ?? null;
  }

  function placeName(target: string | null): string {
    const places = [...folders, ...(data?.path ?? [])];
    return places.find(({ id }) => id === target)?.name ?? "Files";
  }

  async function moveBack(done: Moving[]) {
    try {
      for (const { item, from } of done) {
        await move.mutateAsync({ item, target: from });
      }
      toast({ message: `Moved ${countOf(done)} back` });
    } catch (error) {
      toast({ message: failureMessage(error) });
    }
  }

  /** One at a time, like the trash. Whatever moved before a failure can still be undone. */
  async function moveTo(target: string | null) {
    const moving = (dragging ?? []).filter(({ from }) => from !== target);
    setDragging(null);
    setDropTarget(null);
    const done: Moving[] = [];
    let failure: string | undefined;
    try {
      for (const entry of moving) {
        await move.mutateAsync({ item: entry.item, target });
        done.push(entry);
      }
    } catch (error) {
      failure = failureMessage(error);
    }
    deselect(done.map(({ item }) => item));
    const undo = { label: "Undo", run: () => void moveBack(done) };
    toast({
      message: failure ?? `Moved ${countOf(done)} to ${placeName(target)}`,
      action: done.length > 0 ? undo : undefined,
    });
  }

  /** Dragging a selected item takes the whole selection with it. */
  function dragFrom(item: Item): HTMLAttributes<HTMLElement> {
    return {
      draggable: true,
      onDragStart(event) {
        const items = chosen.has(itemKey(item)) ? selected : [item];
        const moving = items.map((one) => ({ item: one, from: originOf(one) }));
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData(DRAG_TYPE, "items");
        if (items.length > 1) {
          setCountPicture(event.dataTransfer, items.length);
        }
        // After the event, because changing the page during it can cancel the drag.
        setTimeout(() => setDragging(moving));
      },
      onDragEnd() {
        setDragging(null);
        setDropTarget(null);
      },
    };
  }

  /** Only offers a place the drag can actually go. */
  function dropOn(target: string | null): HTMLAttributes<HTMLElement> {
    const intoItself = dragging?.some(({ item }) => item.kind === "folder" && item.id === target);
    if (!dragging || intoItself || dragging.every(({ from }) => from === target)) {
      return {};
    }
    return {
      onDragOver(event) {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        setDropTarget(target ?? TOP_LEVEL);
      },
      onDragLeave(event) {
        if (!(event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget))) {
          setDropTarget(null);
        }
      },
      onDrop(event) {
        event.preventDefault();
        void moveTo(target);
      },
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
      { label: "Move to trash", icon: Trash2, run: () => void moveToTrash([item]) },
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
    return [
      { label: "Download", icon: Download, run: () => download(file) },
      { label: "Select", icon: CheckSquare, run: () => toggle(item) },
      { label: "Rename", icon: Pencil, run: () => setAction({ type: "rename", item, name: file.name }) },
      { label: "Move", icon: FolderInput, run: () => setAction({ type: "move", items: [item], name: file.name }) },
      // An added file is the person's own, and stays in the account it came from.
      ...(canChangeAccount && !file.external && !shared ? [toAccount] : []),
      ...((links?.enabled || shared) && !file.external ? [linkEntry] : []),
      { label: "Move to trash", icon: Trash2, run: () => void moveToTrash([item]) },
    ];
  }

  if (isError && !query) {
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
  const path = data?.path ?? [];
  const selectedFiles = selected.filter((item) => item.kind === "file");
  const canMoveSelection =
    canChangeAccount &&
    selectedFiles.length === selected.length &&
    !files.some((file) => (file.external || shares.has(file.id)) && chosen.has(itemKey({ kind: "file", id: file.id })));
  const countText = selected.length === 1 ? "1 selected" : `${selected.length} selected`;

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
            <span className="mr-auto font-semibold">{countText}</span>
          )}
          <Button variant="ghost" aria-label="Download selected items" onClick={downloadSelected}>
            <Download />
            <span className="hidden sm:inline">Download</span>
          </Button>
          <Button
            variant="ghost"
            aria-label="Move selected items"
            onClick={() => setAction({ type: "move", items: selected, name: countText.replace("selected", selected.length === 1 ? "item" : "items") })}
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
                  fileIds: selectedFiles.map(({ id }) => id),
                  name: countText.replace("selected", selected.length === 1 ? "file" : "files"),
                  from: [
                    ...new Set(
                      files.filter((file) => chosen.has(itemKey({ kind: "file", id: file.id }))).map((file) => file.connection_id),
                    ),
                  ],
                })
              }
            >
              <ArrowLeftRight />
              <span className="hidden sm:inline">Account</span>
            </Button>
          )}
          <Button variant="ghost" aria-label="Move selected items to the trash" onClick={() => void moveToTrash(selected)}>
            <Trash2 />
            <span className="hidden sm:inline">Trash</span>
          </Button>
        </div>
      ) : (
        <div className="mb-[18px] flex min-h-9 items-center gap-2">
          {query ? (
            <h1 className="min-w-0 flex-1 truncate text-xl font-semibold tracking-tight">Results for {query}</h1>
          ) : (
            <Breadcrumbs path={path} dropOn={dropOn} dropTarget={dropTarget} />
          )}
          {!query && (
            <Button variant="outline" aria-label="New folder" onClick={() => setAction({ type: "new-folder" })}>
              <FolderPlus />
              <span className="hidden sm:inline">New folder</span>
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

      <FormError message={viewing ? undefined : trash.error?.message} />
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && query && <FormError message="The search didn't work. Try again." />}

      {empty && query && <Blank title="Nothing matches" text="Try part of the name, or a different word." />}
      {empty && !query && noAccounts && (
        <Blank title="Link an account to start" text="Your files live in your own Google Drive accounts.">
          <Button asChild>
            <Link to="/storage">Link a Google Drive account</Link>
          </Button>
        </Blank>
      )}
      {empty && !query && !noAccounts && (
        <Blank title="This folder is empty" text="Drop files anywhere on this page, or use Upload." />
      )}

      {view === "grid" && !empty && (
        <>
          {folders.length > 0 && (
            <ul className="mb-[22px] grid grid-cols-2 gap-3.5 md:grid-cols-[repeat(auto-fill,minmax(190px,1fr))]">
              {folders.map((target) => {
                const item: Item = { kind: "folder", id: target.id };
                return (
                  <FolderTile
                    key={target.id}
                    value={target}
                    selected={chosen.has(itemKey(item))}
                    selecting={selecting}
                    entries={folderEntries(target)}
                    drag={{ ...dragFrom(item), ...dropOn(target.id) }}
                    lit={dropTarget === target.id}
                    onToggle={() => toggle(item)}
                  />
                );
              })}
            </ul>
          )}
          <ul className="mosaic">
            {files.map((file) => {
              const item: Item = { kind: "file", id: file.id };
              return (
                <FileTile
                  key={file.id}
                  value={file}
                  selected={chosen.has(itemKey(item))}
                  selecting={selecting}
                  entries={fileEntries(file)}
                  drag={dragFrom(item)}
                  shared={shares.has(file.id)}
                  onToggle={() => toggle(item)}
                  onOpen={() => setViewing(file.id)}
                />
              );
            })}
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
            {folders.map((target) => {
              const item: Item = { kind: "folder", id: target.id };
              return (
                <ListRow
                  key={target.id}
                  name={target.name}
                  thumb={
                    <span className="grid size-[38px] shrink-0 place-items-center rounded-[7px] bg-card">
                      <FolderIcon className="size-[18px] text-muted-foreground" />
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
                  selected={chosen.has(itemKey(item))}
                  selecting={selecting}
                  entries={folderEntries(target)}
                  drag={{ ...dragFrom(item), ...dropOn(target.id) }}
                  lit={dropTarget === target.id}
                  onToggle={() => toggle(item)}
                />
              );
            })}
            {files.map((file) => {
              const item: Item = { kind: "file", id: file.id };
              return (
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
                  selected={chosen.has(itemKey(item))}
                  selecting={selecting}
                  entries={fileEntries(file)}
                  drag={dragFrom(item)}
                  shared={shares.has(file.id)}
                  onToggle={() => toggle(item)}
                />
              );
            })}
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
          onTrash={handleViewerTrash}
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
                  : `Moving ${queued === 1 ? "1 file" : `${queued} files`} to ${account}. Follow it on the Storage page.`,
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
export function FilesPage({ folderId, query }: { folderId: string | undefined; query: string | undefined }) {
  return <Browser key={`${folderId ?? "top"}:${query ?? ""}`} folderId={folderId} query={query} />;
}
