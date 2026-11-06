import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useEffect, useState } from "react";

import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { authConfigQuery } from "@/features/auth/queries";
import { useUploads } from "@/features/files/uploads";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

import {
  connectionsQuery,
  poolQuery,
  transfersQuery,
  useCancelTransfer,
  useConnectGoogleDrive,
  useEmptyConnection,
  useRemoveConnection,
  useRetryTransfers,
  useStopEmptying,
  useSyncConnection,
  useTestConnection,
} from "../queries";

type AccountFiles = components["schemas"]["AccountFiles"];
type Connection = components["schemas"]["Connection"];
type Pool = components["schemas"]["Pool"];
type PoolAccount = components["schemas"]["PoolAccount"];
type Transfer = components["schemas"]["Transfer"];
type TransferQueue = components["schemas"]["TransferQueue"];

const REDIRECT_MESSAGES: Record<string, string> = {
  google_cancelled: "Linking the account was cancelled.",
  google_failed: "The account wasn't linked. Try again.",
};

const SOFT_BUTTON = "bg-raised hover:bg-[#3a3a3a]";

function syncText({ added, removed }: { added: number; removed: number }): string {
  if (added === 0 && removed === 0) {
    return "Already up to date.";
  }
  const parts = [];
  if (added > 0) {
    parts.push(added === 1 ? "1 file found" : `${added} files found`);
  }
  if (removed > 0) {
    parts.push(removed === 1 ? "1 file gone from Drive" : `${removed} files gone from Drive`);
  }
  return `${parts.join(", ")}.`;
}

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

function filesText(count: number): string {
  return count === 1 ? "1 file" : `${count} files`;
}

/** What an account being emptied has left to do. */
function emptyingText(files: AccountFiles | undefined, idle: boolean): string {
  const left = files?.files ?? 0;
  if (left > 0) {
    return idle ? `${filesText(left)} still here. Empty it again to move them.` : `Emptying: ${filesText(left)} left`;
  }
  const added = files?.external_files ?? 0;
  if (added > 0) {
    return `Emptied. ${added === 1 ? "1 file added from Drive stays" : `${added} files added from Drive stay`} here, and will leave dextro if you remove the account.`;
  }
  return "Empty. You can remove it now.";
}

/** Says what removing an account costs, which is everything dextro shows from it. */
function removeText(files: AccountFiles | undefined): string {
  const held = (files?.files ?? 0) + (files?.external_files ?? 0);
  if (held === 0) {
    return "dextro will forget its token. Nothing is stored in it.";
  }
  return `dextro will forget its token and stop showing the ${filesText(held)} stored in it, trash included. They stay in that Drive. To keep them in dextro, empty the account first.`;
}

/** How much room an account has. No limit is the most room there is. */
function room(account: PoolAccount): number {
  if (account.limit_bytes === null) {
    return Number.POSITIVE_INFINITY;
  }
  return Math.max(account.limit_bytes - account.used_bytes - account.reserved_bytes, 0);
}

/** The account an upload goes to next: the one picked, or the one with most room. */
function nextAccount(pool: Pool, target: string): string | undefined {
  if (target) {
    return target;
  }
  const working = pool.accounts.filter((account) => account.available && !account.connection.draining);
  if (working.length < 2) {
    return undefined;
  }
  return working.reduce((best, account) => (room(account) > room(best) ? account : best)).connection.id;
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
        {account.reserved_bytes > 0 && `, ${formatBytes(account.reserved_bytes)} held for uploads`}
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
        className={cn("relative h-[130px] overflow-hidden rounded-[10px] bg-[#0a0a0a]", broken && "hatched")}
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
          <Button variant="outline" size="sm" className={SOFT_BUTTON} onClick={() => openPicker(connection.id)}>
            Add from Drive
          </Button>
        )}
        <Button variant="outline" size="sm" className={SOFT_BUTTON} disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? "Syncing…" : "Sync"}
        </Button>
        <Button variant="outline" size="sm" className={SOFT_BUTTON} disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? "Testing…" : "Test"}
        </Button>
        {connection.draining && (
          <Button variant="outline" size="sm" className={SOFT_BUTTON} disabled={stop.isPending} onClick={() => stop.mutate()}>
            Use this account again
          </Button>
        )}
        {canEmpty && !broken && (!connection.draining || (idle && (files?.files ?? 0) > 0)) && (
          <Button variant="outline" size="sm" className={SOFT_BUTTON} onClick={() => setConfirming("empty")}>
            Empty
          </Button>
        )}
        <Button variant="outline" size="sm" className={SOFT_BUTTON} onClick={() => setConfirming("remove")}>
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
          text={`Its ${filesText(files?.files ?? 0)} will move to your other accounts in the background, one at a time, which can take hours for large files. Each is copied and checked, then deleted from this account for good. Nothing new goes in it until you say so. Files added from Drive stay.`}
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
          <button
            type="button"
            className="grid size-6 place-items-center rounded-full hover:bg-raised hover:text-foreground"
            aria-label={`${failed ? "Clear" : "Cancel"} moving ${transfer.name}`}
            disabled={cancel.isPending}
            onClick={() => cancel.mutate(transfer.id)}
          >
            <X className="size-3.5" />
          </button>
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
function Moves({ queue, connections }: { queue: TransferQueue | undefined; connections: Connection[] | undefined }) {
  const retry = useRetryTransfers();
  if (!queue || (queue.transfers.length === 0 && queue.removing === 0)) {
    return null;
  }
  const emails = new Map(connections?.map((c) => [c.id, c.summary]));
  const active = queue.transfers.find((transfer) => transfer.status === "waiting");
  const parts = [];
  if (queue.waiting > 0) {
    parts.push(`${filesText(queue.waiting)} to move, ${formatBytes(queue.waiting_bytes)}`);
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
          <Button variant="outline" size="sm" className={SOFT_BUTTON} disabled={retry.isPending} onClick={() => retry.mutate()}>
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
  const next = pool && nextAccount(pool, target);
  const usable = connections.filter((connection) => !connection.draining).length;
  return (
    <ul className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,210px),1fr))] gap-3.5">
      {connections.map((connection) => (
        <Tank
          key={connection.id}
          connection={connection}
          account={pool?.accounts.find((a) => a.connection.id === connection.id)}
          isNext={connection.id === next}
          pickerEnabled={pool?.picker_enabled ?? false}
          files={queue?.accounts.find((a) => a.connection_id === connection.id)}
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
  const { data: connections } = useQuery(connectionsQuery);
  const connect = useConnectGoogleDrive();
  const queryClient = useQueryClient();
  const [added, setAdded] = useState<string>();

  // The popup says what it added as it closes.
  useEffect(() => {
    function handleMessage(event: MessageEvent<{ type?: string; added: number; skipped: number }>) {
      if (event.origin !== window.location.origin || event.data?.type !== "dextro-files-added") {
        return;
      }
      setAdded(addedText(event.data));
      void queryClient.invalidateQueries({ queryKey: ["files"] });
    }
    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [queryClient]);
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
      <Accounts pool={pool} queue={queue} />
      <Moves queue={queue} connections={connections} />
      {config?.google_enabled ? (
        <p className="max-w-[60ch] text-xs text-faint">
          dextro keeps its files in a folder named dextro in each account, and can't see anything else in
          them. A file is stored whole in one account, so the largest file you can upload is limited by the
          account with the most free space.
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          Google isn't set up on this server. Add GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to link accounts.
        </p>
      )}
    </div>
  );
}
