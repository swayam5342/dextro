import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, X } from "lucide-react";
import { useEffect, useState } from "react";

import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { authConfigQuery } from "@/features/auth/queries";
import { RowButton } from "@/features/files/components/upload-panel";
import { useRefreshFiles } from "@/features/files/queries";
import { useUploads } from "@/features/files/uploads";
import { count, formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

import { nextAccount, room, syncText, usableConnections } from "../pool";
import {
  connectionsQuery,
  poolQuery,
  routingQuery,
  transfersQuery,
  useCancelTransfer,
  useConnectGoogleDrive,
  useEmptyConnection,
  useRemoveConnection,
  useRetryTransfers,
  useSetRouting,
  useStopEmptying,
  useSyncConnection,
  useTestConnection,
} from "../queries";
import { UsageCard } from "./usage-card";

type AccountFiles = components["schemas"]["AccountFiles"];
type Connection = components["schemas"]["Connection"];
type Pool = components["schemas"]["Pool"];
type PoolAccount = components["schemas"]["PoolAccount"];
type RoutingMode = components["schemas"]["RoutingMode"];
type Transfer = components["schemas"]["Transfer"];
type TransferQueue = components["schemas"]["TransferQueue"];

const REDIRECT_MESSAGES: Record<string, string> = {
  google_cancelled: "Linking the account was cancelled.",
  google_failed: "The account wasn't linked. Try again.",
};

/** Google's picker runs in its own popup, which has its own security policy. */
function openPicker(connectionId: string) {
  window.open(`/drive-picker.html?connection=${encodeURIComponent(connectionId)}`, "dextro-picker", "popup,width=1000,height=720");
}

function addedText({ added, skipped }: { added: number; skipped: number }): string {
  const files = added === 1 ? "1 file added." : `${added} files added.`;
  if (skipped === 0) {
    return files;
  }
  const others = skipped === 1 ? "1 was skipped" : `${skipped} were skipped`;
  return `${files} ${others}: Google Docs can't be added, and nor can files that are already here.`;
}

/** What an account being emptied has left to do. */
function emptyingText(files: AccountFiles | undefined, idle: boolean): string {
  const left = files?.files ?? 0;
  if (left > 0) {
    return idle ? `${count(left, "file")} still here. Empty it again to move them.` : `Emptying: ${count(left, "file")} left`;
  }
  const added = files?.external_files ?? 0;
  if (added > 0) {
    return `Emptied. ${added === 1 ? "1 file added from Drive stays" : `${added} files added from Drive stay`} here, and will leave Tributary if you remove the account.`;
  }
  return "Empty. You can remove it now.";
}

/** Says what removing an account costs, which is everything Tributary shows from it. */
function removeText(files: AccountFiles | undefined): string {
  if (!files) {
    return "Tributary will forget its token and stop showing anything stored in it. It all stays in that Drive. To keep it in Tributary, empty the account first.";
  }
  const held = files.files + files.external_files;
  if (held === 0) {
    return "Tributary will forget its token. Nothing is stored in it.";
  }
  return `Tributary will forget its token and stop showing the ${count(held, "file")} stored in it, trash included. They stay in that Drive. To keep them in Tributary, empty the account first.`;
}

const ROUTING_MODES: { mode: RoutingMode; label: string; text: string }[] = [
  { mode: "most_space", label: "Most space", text: "Each file goes to the account with the most free space." },
  { mode: "round_robin", label: "Round robin", text: "Files take turns, so they spread evenly across your accounts." },
  { mode: "priority", label: "Priority order", text: "Files fill your first account, then move on when one won't fit." },
];

/** How uploads choose an account. With one account there's nothing to choose. */
function UploadRouting() {
  const { data: connections } = useQuery(connectionsQuery);
  const { data: routing } = useQuery(routingQuery);
  const save = useSetRouting();
  if (!connections || connections.length < 2 || !routing) {
    return null;
  }
  const { mode, order } = routing;
  function move(from: number, to: number) {
    const next = [...order];
    next.splice(to, 0, ...next.splice(from, 1));
    save.mutate({ mode, order: next });
  }
  return (
    <section aria-label="Upload routing" className="rounded-[14px] bg-card p-3.5">
      <h2 className="font-semibold">Upload routing</h2>
      <p className="text-[13px] text-muted-foreground">Where new files go when you don't pick an account.</p>
      <div role="radiogroup" aria-label="Upload routing" className="mt-3 grid gap-2 sm:grid-cols-3">
        {ROUTING_MODES.map(({ mode: option, label, text }) => (
          <label
            key={option}
            className={cn(
              "flex cursor-pointer items-start gap-2 rounded-[10px] border bg-well p-2.5",
              option === mode && "border-foreground",
            )}
          >
            <input
              type="radio"
              name="upload-routing"
              className="mt-1 accent-foreground"
              checked={option === mode}
              onChange={() => save.mutate({ mode: option, order })}
            />
            <span className="min-w-0">
              <span className="block text-sm font-medium">{label}</span>
              <span className="block text-xs text-muted-foreground">{text}</span>
            </span>
          </label>
        ))}
      </div>
      {mode === "priority" && (
        <ol aria-label="Account order" className="mt-3 grid gap-1">
          {order.map((id, index) => {
            const connection = connections.find((c) => c.id === id);
            const name = connection?.summary ?? "Unknown account";
            return (
              <li key={id} className="flex items-center gap-2 text-[13px]">
                <span className="w-4 shrink-0 text-right text-muted-foreground">{index + 1}</span>
                <span className="min-w-0 flex-1 truncate">{name}</span>
                {connection?.draining && <span className="shrink-0 text-xs text-faint">Being emptied, so skipped</span>}
                <RowButton label={`Move ${name} up`} disabled={index === 0} onClick={() => move(index, index - 1)}>
                  <ChevronUp className="size-4" />
                </RowButton>
                <RowButton
                  label={`Move ${name} down`}
                  disabled={index === order.length - 1}
                  onClick={() => move(index, index + 1)}
                >
                  <ChevronDown className="size-4" />
                </RowButton>
              </li>
            );
          })}
        </ol>
      )}
      <FormError message={save.error ? "That wasn't saved. Try again." : undefined} />
    </section>
  );
}

function Figure({ account }: { account: PoolAccount | undefined }) {
  if (!account) {
    return <p className="text-sm text-muted-foreground">Checking free space…</p>;
  }
  if (!account.available) {
    return (
      <div>
        <b className="block text-2xl leading-tight font-semibold tracking-tight">Not reachable</b>
        <span className="text-xs text-muted-foreground">Uploads skip this account</span>
      </div>
    );
  }
  if (account.limit_bytes === null) {
    return (
      <div>
        <b className="block text-2xl leading-tight font-semibold tracking-tight">{formatBytes(account.used_bytes)}</b>
        <span className="text-xs text-muted-foreground">used, no limit</span>
      </div>
    );
  }
  return (
    <div>
      <b className="block text-2xl leading-tight font-semibold tracking-tight">{formatBytes(room(account))}</b>
      <span className="text-xs text-muted-foreground">free of {formatBytes(account.limit_bytes)}
        {account.reserved_bytes > 0 && `, ${formatBytes(account.reserved_bytes)} held for uploads and moves`}
      </span>
    </div>
  );
}

function Tank({
  connection,
  account,
  isNext,
  pickerEnabled,
  files,
  canEmpty,
  idle,
}: {
  connection: Connection;
  account: PoolAccount | undefined;
  isNext: boolean;
  pickerEnabled: boolean;
  files: AccountFiles | undefined;
  canEmpty: boolean;
  /** Nothing is waiting to move, so files left here aren't on their way. */
  idle: boolean;
}) {
  const test = useTestConnection(connection.id);
  const remove = useRemoveConnection(connection.id);
  const sync = useSyncConnection(connection.id);
  const empty = useEmptyConnection(connection.id);
  const stop = useStopEmptying(connection.id);
  const [confirming, setConfirming] = useState<"remove" | "empty" | null>(null);
  const broken = account?.available === false;
  const limit = account?.limit_bytes;
  const fill = account?.available && limit ? Math.min((account.used_bytes / limit) * 100, 100) : 0;

  return (
    <li className="flex min-w-0 flex-col gap-3 rounded-[14px] bg-card p-3.5">
      <Figure account={account} />
      <div
        role="img"
        aria-label={broken ? `${connection.summary} can't be reached` : `${Math.round(fill)}% of ${connection.summary} is used`}
        className={cn("relative h-[130px] overflow-hidden rounded-[10px] bg-well", broken && "hatched")}
      >
        {fill > 0 && (
          <span
            className="absolute inset-x-0 bottom-0 border-t-2 border-water bg-water-deep"
            style={{ height: `${fill}%` }}
          />
        )}
      </div>
      <h2 className="truncate text-sm font-medium" title={connection.summary}>
        {connection.summary}
      </h2>
      {isNext && <p className="text-[13px] text-water">Next upload goes here</p>}
      {connection.draining && <p className="text-[13px]">{emptyingText(files, idle)}</p>}
      {broken && <p className="text-[13px] text-destructive">Link it again to repair it.</p>}
      {test.isSuccess && <p className="text-[13px]">This account works.</p>}
      {sync.data && <p className="text-[13px]">{syncText(sync.data)}</p>}
      <FormError message={sync.error ? "This account couldn't be checked, so nothing changed." : undefined} />
      <FormError message={test.error ? "This account couldn't be reached. Link it again to repair it." : undefined} />
      <FormError message={stop.error?.message} />
      <div className="mt-auto flex flex-wrap gap-2">
        {pickerEnabled && !broken && (
          <Button variant="soft" size="sm" onClick={() => openPicker(connection.id)}>
            Add from Drive
          </Button>
        )}
        <Button variant="soft" size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? "Syncing…" : "Sync"}
        </Button>
        <Button variant="soft" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? "Testing…" : "Test"}
        </Button>
        {connection.draining && (
          <Button variant="soft" size="sm" disabled={stop.isPending} onClick={() => stop.mutate()}>
            Use this account again
          </Button>
        )}
        {canEmpty && !broken && (!connection.draining || (idle && (files?.files ?? 0) > 0)) && (
          <Button variant="soft" size="sm" onClick={() => setConfirming("empty")}>
            Empty
          </Button>
        )}
        <Button variant="soft" size="sm" onClick={() => setConfirming("remove")}>
          Remove
        </Button>
      </div>
      {confirming === "remove" && (
        <ConfirmDialog
          title={`Remove ${connection.summary}?`}
          text={removeText(files)}
          action="Remove account"
          pending={remove.isPending}
          error={remove.error?.message}
          onConfirm={() => remove.mutate(undefined, { onSuccess: () => setConfirming(null) })}
          onClose={() => {
            setConfirming(null);
            remove.reset();
          }}
        />
      )}
      {confirming === "empty" && (
        <ConfirmDialog
          safe
          title={`Empty ${connection.summary}?`}
          text={`Its ${count(files?.files ?? 0, "file")} will move to your other accounts in the background, one at a time, which can take hours for large files. Each is copied and checked, then deleted from this account for good. Nothing new goes in it until you say so. Files added from Drive stay.`}
          action="Empty account"
          pending={empty.isPending}
          error={empty.error?.message}
          onConfirm={() => empty.mutate(undefined, { onSuccess: () => setConfirming(null) })}
          onClose={() => {
            setConfirming(null);
            empty.reset();
          }}
        />
      )}
    </li>
  );
}

function MoveRow({ transfer, active, emails }: { transfer: Transfer; active: boolean; emails: Map<string, string> }) {
  const cancel = useCancelTransfer();
  const failed = transfer.status === "failed";
  const percent = transfer.size > 0 ? Math.round((transfer.copied / transfer.size) * 100) : 0;
  const route = `${emails.get(transfer.source_connection_id) ?? "Unknown account"} to ${emails.get(transfer.target_connection_id) ?? "Unknown account"}`;
  return (
    <li className="space-y-1">
      <div className="flex items-center justify-between gap-3 text-[13px]">
        <span className="min-w-0 truncate">{transfer.name}</span>
        <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
          {failed ? "Failed" : active ? `${percent}%` : "Waiting"}
          <RowButton
            label={`${failed ? "Clear" : "Cancel"} moving ${transfer.name}`}
            disabled={cancel.isPending}
            onClick={() => cancel.mutate(transfer.id)}
          >
            <X className="size-3.5" />
          </RowButton>
        </span>
      </div>
      {active && <progress className="level block" value={percent} max={100} aria-label={`Progress of ${transfer.name}`} />}
      {failed ? (
        <p role="alert" className="text-xs text-destructive">
          {transfer.failure}
        </p>
      ) : (
        <p className="truncate text-xs text-faint">{route}</p>
      )}
    </li>
  );
}

/** Files on their way between accounts. Only shows when there's something to show. */
function Moves({ queue }: { queue: TransferQueue | undefined }) {
  const retry = useRetryTransfers();
  const { data: connections } = useQuery(connectionsQuery);
  if (!queue || (queue.transfers.length === 0 && queue.removing === 0)) {
    return null;
  }
  const emails = new Map(connections?.map((c) => [c.id, c.summary]));
  const active = queue.transfers.find((transfer) => transfer.status === "waiting");
  const parts = [];
  if (queue.waiting > 0) {
    parts.push(`${count(queue.waiting, "file")} to move, ${formatBytes(queue.waiting_bytes)}`);
  }
  if (queue.failed > 0) {
    parts.push(`${queue.failed} failed`);
  }
  if (queue.removing > 0) {
    parts.push(queue.removing === 1 ? "1 old copy to remove" : `${queue.removing} old copies to remove`);
  }
  return (
    <section aria-label="Moving files" className="rounded-[14px] bg-card p-3.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <h2 className="font-semibold">Moving files</h2>
          <p className="text-[13px] text-muted-foreground">{parts.join(", ")}</p>
        </div>
        {queue.failed > 0 && (
          <Button variant="soft" size="sm" disabled={retry.isPending} onClick={() => retry.mutate()}>
            Try failed moves again
          </Button>
        )}
      </div>
      <FormError message={retry.error?.message} />
      {queue.transfers.length > 0 && (
        <ul className="mt-3 grid max-h-72 gap-3 overflow-y-auto">
          {queue.transfers.map((transfer) => (
            <MoveRow key={transfer.id} transfer={transfer} active={transfer.id === active?.id} emails={emails} />
          ))}
        </ul>
      )}
    </section>
  );
}

function PoolSummary({ pool }: { pool: Pool }) {
  const working = pool.accounts.filter((account) => account.available).length;
  const accounts = working === 1 ? "1 account" : `${working} accounts`;
  const text =
    pool.free_bytes === null || pool.limit_bytes === null
      ? `${formatBytes(pool.used_bytes)} used across ${accounts}`
      : `${formatBytes(pool.free_bytes)} free of ${formatBytes(pool.limit_bytes)} across ${accounts}`;
  return <p className="text-muted-foreground">{text}</p>;
}

function Accounts({ pool, queue }: { pool: Pool | undefined; queue: TransferQueue | undefined }) {
  const { data: connections, isPending, isError } = useQuery(connectionsQuery);
  const { data: routing } = useQuery(routingQuery);
  const { target } = useUploads();

  if (isPending) {
    return <p className="text-muted-foreground">Loading your accounts…</p>;
  }
  if (isError) {
    return <FormError message="Your accounts couldn't be loaded. Reload the page to try again." />;
  }
  if (connections.length === 0) {
    return <p className="py-6 text-muted-foreground">No accounts are linked yet. Link a Google Drive account to start storing files.</p>;
  }
  const next = pool && nextAccount(pool, routing, target);
  const usable = usableConnections(connections).length;
  return (
    <ul className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,210px),1fr))] gap-3.5">
      {connections.map((connection) => (
        <Tank
          key={connection.id}
          connection={connection}
          account={pool?.accounts.find((a) => a.connection.id === connection.id)}
          isNext={connection.id === next}
          pickerEnabled={pool?.picker_enabled ?? false}
          // An account with nothing in it has no row, which isn't the same as not knowing yet.
          files={
            queue &&
            (queue.accounts.find((a) => a.connection_id === connection.id) ?? {
              connection_id: connection.id,
              files: 0,
              external_files: 0,
            })
          }
          canEmpty={usable > (connection.draining ? 0 : 1)}
          idle={(queue?.waiting ?? 0) === 0}
        />
      ))}
    </ul>
  );
}

export function StoragePage({
  redirectError,
  connected,
}: {
  redirectError: string | undefined;
  connected: string | undefined;
}) {
  const { data: config } = useQuery(authConfigQuery);
  const { data: pool } = useQuery(poolQuery);
  const { data: queue } = useQuery(transfersQuery);
  const connect = useConnectGoogleDrive();
  const refreshFiles = useRefreshFiles();
  const [added, setAdded] = useState<string>();

  // The popup says what it added as it closes.
  useEffect(() => {
    function handleMessage(event: MessageEvent<{ type?: string; added: number; skipped: number }>) {
      if (event.origin !== window.location.origin || event.data?.type !== "dextro-files-added") {
        return;
      }
      setAdded(addedText(event.data));
      void refreshFiles();
    }
    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [refreshFiles]);
  const message = redirectError
    ? (REDIRECT_MESSAGES[redirectError] ?? REDIRECT_MESSAGES.google_failed)
    : undefined;

  return (
    <div className="max-w-5xl space-y-[18px]">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">Storage</h1>
          {pool && pool.accounts.length > 0 && <PoolSummary pool={pool} />}
        </div>
        {config?.google_enabled && (
          <Button disabled={connect.isPending} onClick={() => connect.mutate()}>
            Link a Google Drive account
          </Button>
        )}
      </div>
      <FormError message={message ?? connect.error?.message} />
      {connected === "google" && <p>Account linked.</p>}
      {added && <p role="status">{added}</p>}
      <UsageCard className="rounded-[14px] bg-card p-3.5 md:hidden" />
      <Accounts pool={pool} queue={queue} />
      <UploadRouting />
      <Moves queue={queue} />
      {config?.google_enabled ? (
        <p className="max-w-[60ch] text-xs text-faint">
          Tributary keeps its files in a folder named dextro in each account, and can't see anything else in
          them. A file is stored whole in one account, so the largest file you can upload is limited by the
          account with the most free space, however uploads are routed.
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          Google isn't set up on this server. Add GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to link accounts.
        </p>
      )}
    </div>
  );
}
