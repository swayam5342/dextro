import { Link } from "@tanstack/react-router";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Check,
  Folder as FolderIcon,
  Link2,
  MoreVertical,
  Play,
  Star,
  type LucideIcon,
} from "lucide-react";
import { useState, type HTMLAttributes, type MouseEvent } from "react";

import type { components } from "@/api/schema";
import { Mark } from "@/components/mark";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@/components/ui/context-menu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { formatBytes, formatDate } from "@/lib/format";
import { cn } from "@/lib/utils";

import { thumbnailUrl } from "../queries";
import { nextSort, SORTS, type SortField, type SortId } from "../sort";
import { TOP_LEVEL } from "../use-drag-move";
import { fileIcon, fileTint } from "./file-icon";

type File = components["schemas"]["File"];
type Folder = components["schemas"]["Folder"];

export interface MenuEntry {
  label: string;
  icon: LucideIcon;
  run: () => void;
}

const SHOW_ON_TOUCH = "[@media(hover:none)]:opacity-100";

export function Breadcrumbs({
  path,
  dropOn,
  dropTarget,
}: {
  path: Folder[];
  dropOn: (target: string | null) => HTMLAttributes<HTMLElement>;
  dropTarget: string | null;
}) {
  const title = "text-2xl font-semibold tracking-tight";
  const lit = "rounded-md text-foreground outline-2 outline-offset-2 outline-foreground";
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

export function SortMenu({ sort, onSort }: { sort: SortId; onSort: (sort: SortId) => void }) {
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

export function SortHeading({
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

function StarBadge({ className }: { className?: string }) {
  return (
    <span role="img" aria-label="Starred" title="Starred" className={cn("grid size-6 shrink-0 place-items-center rounded-full", className)}>
      <Star className="size-3.5 fill-current" />
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
        className={cn("grid size-[30px] shrink-0 place-items-center rounded-full text-foreground", className)}
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
        "grid size-6 shrink-0 place-items-center rounded-full border-[1.5px] border-on-media bg-media-scrim text-on-media opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100",
        (visible || selected) && "opacity-100",
        selected && "bg-on-media text-on-media-ink",
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

export function FolderTile({
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
            "group relative flex min-w-0 items-center gap-1 rounded-[14px] bg-card pr-2 transition-[background-color,transform] duration-150 hover:scale-[1.012] hover:bg-accent",
            selected && "outline-2 outline-foreground",
            lit && "bg-raised outline-2 outline-foreground",
          )}
        >
          <Link
            to="/"
            search={{ folder: folder.id }}
            draggable={false}
            onClick={handleClick}
            className="flex min-w-0 flex-1 items-center gap-3 py-3 pl-3.5"
          >
            <FolderIcon className="size-[22px] shrink-0 fill-raised text-foreground" />
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

export function FileTile({
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
            "group relative h-(--row) min-w-0 overflow-hidden rounded-[10px] bg-card transition-transform duration-150 hover:scale-[1.012]",
            selected && "outline-2 outline-foreground",
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
                <Icon className={cn("size-[22px] md:size-[30px]", fileTint(file.content_type))} />
                <span className="min-w-0">
                  <span className="line-clamp-1 leading-tight font-medium break-words md:line-clamp-2">{file.name}</span>
                  <span className="mt-0.5 block text-xs text-faint">{formatBytes(file.size)}</span>
                </span>
              </span>
            )}
          </button>
          {hasPicture && file.preview === "video" && (
            <span className="pointer-events-none absolute top-1/2 left-1/2 grid size-[46px] -translate-1/2 place-items-center rounded-full bg-media-scrim">
              <Play className="ml-0.5 size-5 fill-on-media stroke-none" />
            </span>
          )}
          {hasPicture && (
            <span className="pointer-events-none absolute inset-x-0 bottom-0 truncate bg-linear-to-t from-media-fade to-transparent px-2.5 pt-6.5 pb-2 text-[12.5px] text-on-media opacity-0 transition-opacity group-hover:opacity-100">
              {file.name}
            </span>
          )}
          {shared && <LinkBadge className="pointer-events-none absolute right-2 bottom-2 bg-media-scrim text-on-media" />}
          {file.starred && <StarBadge className="pointer-events-none absolute bottom-2 left-2 bg-media-scrim text-on-media" />}
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
              "absolute top-1.5 right-1.5 bg-media-scrim text-on-media opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
              SHOW_ON_TOUCH,
            )}
          />
        </li>
      </ContextMenuTrigger>
      <RightClickMenu entries={entries} />
    </ContextMenu>
  );
}

export function ListThumb({ file }: { file: File }) {
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
        <Icon className={cn("size-[18px]", fileTint(file.content_type))} />
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
  starred?: boolean;
  onToggle: () => void;
}

export function ListRow({
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
  starred,
  onToggle,
}: ListRowProps) {
  const cell = "border-t px-3 py-2 whitespace-nowrap text-muted-foreground group-hover:bg-row-hover";
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
              {starred && <StarBadge className="text-muted-foreground" />}
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

export function Blank({ title, text, children }: { title: string; text: string; children?: React.ReactNode }) {
  return (
    <div className="page-in grid justify-items-start gap-1 py-10">
      <Mark className="mb-3 size-10" />
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <p className="text-muted-foreground">{text}</p>
      {children && <div className="mt-3">{children}</div>}
    </div>
  );
}
