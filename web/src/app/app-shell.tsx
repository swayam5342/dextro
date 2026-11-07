import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useSearch } from "@tanstack/react-router";
import {
  Activity,
  Check,
  ChevronDown,
  Clock,
  Copy,
  Database,
  Folder,
  FolderUp,
  Link2,
  ScrollText,
  Search,
  Settings,
  Star,
  Trash2,
  type LucideIcon,
  Upload,
  UserRound,
  Users,
  X,
} from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";

import { Mark } from "@/components/mark";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { meQuery, useLogout } from "@/features/auth/queries";
import { ThemeButton } from "@/components/theme-button";
import { ToastProvider } from "@/components/toast";
import { DropZone, UploadPanel } from "@/features/files/components/upload-panel";
import { treeFromPicked } from "@/features/files/folder-tree";
import { fileChecksQuery } from "@/features/files/queries";
import { UploadsProvider, useUploads } from "@/features/files/uploads";
import { UsageCard } from "@/features/storage/components/usage-card";
import { accountAlert, freeSpaceText, problemAlert, routingText, usableConnections } from "@/features/storage/pool";
import { connectionsQuery, poolQuery, routingQuery, useFollowTransfers } from "@/features/storage/queries";
import { recall, remember } from "@/lib/remember";
import { keyIsTaken } from "@/lib/utils";

function NavLink({ to, icon: Icon, label }: { to: "/" | "/recent" | "/starred" | "/trash" | "/links" | "/duplicates" | "/storage" | "/admin/settings" | "/admin/users" | "/admin/status" | "/admin/audit"; icon: LucideIcon; label: string }) {
  return (
    <Link
      to={to}
      search={{}}
      activeOptions={{ exact: to === "/", includeSearch: false }}
      className="flex h-9 items-center gap-3 rounded-[10px] px-2.5 text-sm text-nav transition-colors hover:bg-accent"
      activeProps={{ className: "bg-card font-medium text-foreground" }}
    >
      <Icon className="size-[18px] shrink-0" />
      {label}
    </Link>
  );
}

/** What's taking up the pool, on every page. */
function PoolMeter() {
  const { data: pool } = useQuery(poolQuery);
  if (!pool || pool.accounts.length === 0) {
    return (
      <Link to="/storage" className="mt-auto block rounded-2xl bg-card p-3.5 text-sm text-muted-foreground hover:bg-accent">
        Link a Google Drive account to start your pool.
      </Link>
    );
  }
  return (
    <Link to="/storage" className="mt-auto block rounded-2xl bg-card p-3.5 transition-colors hover:bg-accent">
      <UsageCard />
    </Link>
  );
}

function UserRow() {
  const navigate = useNavigate();
  const { data: user } = useQuery(meQuery);
  const logout = useLogout();
  if (!user) {
    return null;
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex w-full items-center gap-2.5 rounded-xl p-2 text-left hover:bg-accent">
        <span className="grid size-[30px] shrink-0 place-items-center rounded-full bg-avatar text-[13px] font-semibold text-avatar-foreground">
          {user.email.charAt(0).toUpperCase()}
        </span>
        <span className="min-w-0 leading-tight">
          <span className="block truncate text-sm">{user.email}</span>
          <span className="block text-xs text-faint">{user.role === "admin" ? "Admin" : "Member"}</span>
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start">
        <DropdownMenuItem onSelect={() => navigate({ to: "/account" })}>Account</DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => logout.mutate(undefined, { onSuccess: () => navigate({ to: "/sign-in" }) })}
        >
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Sidebar() {
  const { data: user } = useQuery(meQuery);
  return (
    <aside className="hidden min-h-0 flex-col overflow-y-auto border-r p-3 md:flex">
      <nav aria-label="Main" className="flex flex-col gap-0.5">
        <NavLink to="/" icon={Folder} label="Files" />
        <NavLink to="/recent" icon={Clock} label="Recent" />
        <NavLink to="/starred" icon={Star} label="Starred" />
        <NavLink to="/trash" icon={Trash2} label="Trash" />
        <NavLink to="/links" icon={Link2} label="Links" />
        <NavLink to="/duplicates" icon={Copy} label="Duplicates" />
        <NavLink to="/storage" icon={Database} label="Storage" />
      </nav>
      {user?.role === "admin" && (
        <>
          <p className="mx-2.5 mt-5 mb-1.5 text-xs text-faint">Admin</p>
          <nav aria-label="Admin" className="flex flex-col gap-0.5">
            <NavLink to="/admin/users" icon={Users} label="Users" />
            <NavLink to="/admin/settings" icon={Settings} label="Server settings" />
            <NavLink to="/admin/status" icon={Activity} label="Server status" />
            <NavLink to="/admin/audit" icon={ScrollText} label="Audit log" />
          </nav>
        </>
      )}
      <PoolMeter />
      <div className="pt-2">
        <UserRow />
      </div>
    </aside>
  );
}

function SearchBox() {
  const navigate = useNavigate();
  const { q } = useSearch({ strict: false });
  const [text, setText] = useState(q ?? "");
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => setText(q ?? ""), [q]);

  useEffect(() => {
    function focusOnSlash(event: KeyboardEvent) {
      if (event.key === "/" && !event.ctrlKey && !event.metaKey && !event.altKey && !keyIsTaken(event.target)) {
        event.preventDefault();
        box.current?.focus();
      }
    }
    window.addEventListener("keydown", focusOnSlash);
    return () => window.removeEventListener("keydown", focusOnSlash);
  }, []);

  function search(value: string) {
    const trimmed = value.trim();
    void navigate({ to: "/", search: trimmed ? { q: trimmed } : {}, replace: Boolean(q) });
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    search(text);
  }

  return (
    <form role="search" onSubmit={handleSubmit} className="min-w-0 flex-1 md:max-w-[560px]">
      <label className="flex h-[38px] items-center gap-2.5 rounded-full bg-card px-3.5 text-muted-foreground transition-colors focus-within:bg-raised">
        <Search className="size-[18px] shrink-0" />
        <input
          ref={box}
          type="search"
          aria-label="Search your files"
          placeholder="Search your files"
          value={text}
          onChange={(event) => {
            setText(event.target.value);
            if (event.target.value === "" && q) {
              search("");
            }
          }}
          className="w-full min-w-0 bg-transparent text-sm text-foreground outline-none placeholder:text-faint"
        />
      </label>
    </form>
  );
}

function UploadButton({ folderId }: { folderId: string | undefined }) {
  const { add, addTree, target, setTarget } = useUploads();
  const { data: connections } = useQuery(connectionsQuery);
  const { data: pool } = useQuery(poolQuery);
  const picker = useRef<HTMLInputElement>(null);
  const folderPicker = useRef<HTMLInputElement | null>(null);
  // An account being emptied takes nothing new.
  const usable = usableConnections(connections);
  const hasAccounts = usable.length > 0;
  const chosen = usable.find((c) => c.id === target);

  function handlePick(event: ChangeEvent<HTMLInputElement>) {
    const picked = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (picked.length > 0) {
      add(picked, folderId);
    }
  }

  function handleFolderPick(event: ChangeEvent<HTMLInputElement>) {
    const picked = Array.from(event.target.files ?? []);
    event.target.value = "";
    const name = picked[0]?.webkitRelativePath.split("/")[0];
    if (name) {
      void addTree(name, treeFromPicked(picked), folderId);
    }
  }

  return (
    <div className="flex shrink-0 gap-px">
      <input ref={picker} type="file" multiple hidden aria-label="Choose files" onChange={handlePick} />
      <input
        ref={(input) => {
          folderPicker.current = input;
          if (input) {
            // React has no prop for this.
            input.webkitdirectory = true;
          }
        }}
        type="file"
        hidden
        aria-label="Choose a folder"
        onChange={handleFolderPick}
      />
      <Button
        className="rounded-r-none pr-3"
        disabled={!hasAccounts}
        title={hasAccounts ? undefined : "Link a Google Drive account first"}
        onClick={() => picker.current?.click()}
      >
        <Upload />
        <span className="hidden sm:inline">Upload</span>
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button className="rounded-l-none px-2.5" disabled={!hasAccounts} aria-label="More upload options">
            <ChevronDown />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent className="w-80 max-w-[calc(100vw-2rem)]">
          <DropdownMenuItem onSelect={() => folderPicker.current?.click()}>
            <FolderUp />
            Upload a folder
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel>Upload to</DropdownMenuLabel>
          <DropdownMenuItem onSelect={() => setTarget("")}>
            <span className="flex-1">Most free space</span>
            {!chosen && <Check className="text-water" />}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {usable.map((connection) => (
            <DropdownMenuItem key={connection.id} onSelect={() => setTarget(connection.id)}>
              <span className="min-w-0 flex-1 truncate">{connection.summary}</span>
              <span className="shrink-0 text-xs text-muted-foreground">{freeSpaceText(pool?.accounts.find((a) => a.connection.id === connection.id))}</span>
              {target === connection.id && <Check className="text-water" />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

const DISMISSED_KEY = "dextro.dismissed-alert";

/** Says when an account needs attention, everywhere but the page that already shows it. */
function AccountAlert() {
  const { pathname } = useLocation();
  const { data: pool } = useQuery(poolQuery);
  const { data: checks } = useQuery(fileChecksQuery);
  const [dismissed, setDismissed] = useState(() => recall(DISMISSED_KEY));
  const alert = accountAlert(pool) ?? problemAlert(checks?.problems.length ?? 0);
  if (!alert || alert.text === dismissed || pathname === "/storage") {
    return null;
  }
  function dismiss(text: string) {
    setDismissed(text);
    remember(DISMISSED_KEY, text);
  }
  return (
    <div role="status" className="mb-4 flex items-center gap-3 rounded-[14px] bg-card py-2 pr-2 pl-3.5 text-sm">
      <span className="min-w-0 flex-1">{alert.text}</span>
      <Button asChild variant="soft" size="sm">
        <Link to="/storage">{alert.action}</Link>
      </Button>
      <Button variant="ghost" size="icon" aria-label="Dismiss" onClick={() => dismiss(alert.text)}>
        <X />
      </Button>
    </div>
  );
}

function MobileTabs() {
  const tab = (to: "/" | "/trash" | "/links" | "/storage" | "/account", Icon: LucideIcon, label: string) => (
    <Link
      to={to}
      search={{}}
      activeOptions={{ exact: to === "/", includeSearch: false }}
      className="grid content-center justify-items-center gap-0.5 text-[11px] text-nav"
      activeProps={{ className: "text-foreground" }}
    >
      <Icon className="size-[18px]" />
      {label}
    </Link>
  );
  return (
    <nav aria-label="Main" className="grid grid-cols-5 border-t bg-background md:hidden">
      {tab("/", Folder, "Files")}
      {tab("/trash", Trash2, "Trash")}
      {tab("/links", Link2, "Links")}
      {tab("/storage", Database, "Storage")}
      {tab("/account", UserRound, "Account")}
    </nav>
  );
}

function Frame() {
  const { folder } = useSearch({ strict: false });
  const { pathname } = useLocation();
  const { target } = useUploads();
  const { data: connections } = useQuery(connectionsQuery);
  const { data: routing } = useQuery(routingQuery);
  const folderId = pathname === "/" ? folder : undefined;
  const chosen = connections?.find((c) => c.id === target);
  const hint = `Goes to ${chosen ? chosen.summary : routingText(routing?.mode)}`;
  const canDrop = usableConnections(connections).length > 0;
  useFollowTransfers();

  return (
    <div className="grid h-full grid-cols-[minmax(0,1fr)] grid-rows-[56px_minmax(0,1fr)_58px] md:grid-cols-[224px_minmax(0,1fr)] md:grid-rows-[64px_minmax(0,1fr)]">
      <Link to="/" search={{}} className="hidden items-center gap-2.5 border-r border-b px-5 text-[17px] font-semibold tracking-tight md:flex">
        <Mark className="size-6" />
        Tributary
      </Link>
      <header className="flex min-w-0 items-center gap-3 border-b px-3 md:px-6">
        <SearchBox />
        <span className="hidden flex-1 md:block" />
        <ThemeButton />
        <UploadButton folderId={folderId} />
      </header>
      <Sidebar />
      <main id="main" className="relative min-w-0 overflow-y-auto px-4 pt-4 pb-24 md:px-7 md:pt-[22px]">
        <AccountAlert />
        <div key={pathname} className="page-in">
          <Outlet />
        </div>
      </main>
      <MobileTabs />
      <UploadPanel />
      {canDrop && <DropZone folderId={folderId} hint={hint} />}
    </div>
  );
}

/** The frame around every signed-in page. */
export function AppShell() {
  return (
    <ToastProvider>
      <UploadsProvider>
        <Frame />
      </UploadsProvider>
    </ToastProvider>
  );
}
