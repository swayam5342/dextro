import { useQuery } from "@tanstack/react-query";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { authConfigQuery } from "@/features/auth/queries";
import { formatBytes } from "@/lib/format";

import {
  connectionsQuery,
  poolQuery,
  useConnectGoogleDrive,
  useRemoveConnection,
  useTestConnection,
} from "../queries";

type Connection = components["schemas"]["Connection"];
type Pool = components["schemas"]["Pool"];
type PoolAccount = components["schemas"]["PoolAccount"];

const REDIRECT_MESSAGES: Record<string, string> = {
  google_cancelled: "Linking the account was cancelled.",
  google_failed: "The account wasn't linked. Try again.",
};

function usageText(account: PoolAccount | undefined): string {
  if (!account) {
    return "Checking free space…";
  }
  if (!account.available) {
    return "This account can't be reached, so uploads skip it. Link it again to repair it.";
  }
  if (account.limit_bytes === null) {
    return `${formatBytes(account.used_bytes)} used, no limit`;
  }
  const free = Math.max(account.limit_bytes - account.used_bytes, 0);
  return `${formatBytes(free)} free of ${formatBytes(account.limit_bytes)}`;
}

function AccountRow({ connection, account }: { connection: Connection; account: PoolAccount | undefined }) {
  const test = useTestConnection(connection.id);
  const remove = useRemoveConnection(connection.id);
  const broken = account?.available === false;

  function handleRemove() {
    const confirmed = window.confirm(
      `Remove ${connection.summary}? dextro will forget its token and stop showing the files stored in it. The files stay in that Drive.`,
    );
    if (confirmed) {
      remove.mutate();
    }
  }

  return (
    <li className="space-y-2 py-4 first:pt-0">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{connection.summary}</p>
          <p className={broken ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>
            {usageText(account)}
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test"}
          </Button>
          <Button variant="outline" size="sm" disabled={remove.isPending} onClick={handleRemove}>
            Remove
          </Button>
        </div>
      </div>
      {account?.available && account.limit_bytes !== null && (
        <progress
          value={account.used_bytes}
          max={account.limit_bytes}
          aria-label={`Space used in ${connection.summary}`}
          className="h-1 w-full overflow-hidden rounded-full [&::-moz-progress-bar]:bg-primary [&::-webkit-progress-bar]:bg-muted [&::-webkit-progress-value]:bg-primary"
        />
      )}
      {test.isSuccess && <p className="text-sm">This account works.</p>}
      <FormError message={test.error ? "This account couldn't be reached. Link it again to repair it." : undefined} />
      <FormError message={remove.error?.message} />
    </li>
  );
}

function PoolSummary({ pool }: { pool: Pool }) {
  const working = pool.accounts.filter((account) => account.available).length;
  const accounts = working === 1 ? "1 account" : `${working} accounts`;
  if (pool.free_bytes === null || pool.limit_bytes === null) {
    return (
      <p className="text-muted-foreground">
        {formatBytes(pool.used_bytes)} used across {accounts}
      </p>
    );
  }
  return (
    <p className="text-muted-foreground">
      {formatBytes(pool.free_bytes)} free of {formatBytes(pool.limit_bytes)} across {accounts}
    </p>
  );
}

function AccountList() {
  const { data: connections, isPending, isError } = useQuery(connectionsQuery);
  const { data: pool } = useQuery(poolQuery);

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Loading your accounts…</p>;
  }
  if (isError) {
    return <FormError message="Your accounts couldn't be loaded. Reload the page to try again." />;
  }
  if (connections.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No accounts are linked yet. Link a Google Drive account to start storing files.
      </p>
    );
  }
  return (
    <ul className="divide-y">
      {connections.map((connection) => (
        <AccountRow
          key={connection.id}
          connection={connection}
          account={pool?.accounts.find((a) => a.connection.id === connection.id)}
        />
      ))}
    </ul>
  );
}

function LinkAccount() {
  const connect = useConnectGoogleDrive();
  return (
    <section className="space-y-3 border-t pt-6">
      <h2 className="font-medium">Link an account</h2>
      <p className="text-sm text-muted-foreground">
        Every linked account joins your pool. dextro keeps its files in a folder named dextro in each one,
        and can't see anything else in them. A file is stored whole in one account, so the largest file
        you can upload is limited by the account with the most free space.
      </p>
      <FormError message={connect.error?.message} />
      <Button variant="outline" disabled={connect.isPending} onClick={() => connect.mutate()}>
        Link a Google Drive account
      </Button>
    </section>
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
  const message = redirectError
    ? (REDIRECT_MESSAGES[redirectError] ?? REDIRECT_MESSAGES.google_failed)
    : undefined;

  return (
    <div className="max-w-2xl space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Storage</h1>
        {pool && pool.accounts.length > 0 && <PoolSummary pool={pool} />}
      </div>
      <FormError message={message} />
      {connected === "google" && <p className="text-sm">Account linked.</p>}
      <AccountList />
      {config?.google_enabled ? (
        <LinkAccount />
      ) : (
        <p className="text-sm text-muted-foreground">
          Google isn't set up on this server. Add GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to link accounts.
        </p>
      )}
    </div>
  );
}
