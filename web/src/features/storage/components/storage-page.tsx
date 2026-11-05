import { useQuery } from "@tanstack/react-query";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { authConfigQuery } from "@/features/auth/queries";

import {
  connectionsQuery,
  useConnectGoogleDrive,
  useRemoveConnection,
  useTestConnection,
} from "../queries";
import { S3Form } from "./s3-form";

type Connection = components["schemas"]["Connection"];

const REDIRECT_MESSAGES: Record<string, string> = {
  google_cancelled: "Connecting Google Drive was cancelled.",
  google_failed: "Google Drive wasn't connected. Try again.",
};

function ConnectionRow({ connection }: { connection: Connection }) {
  const test = useTestConnection(connection.id);
  const remove = useRemoveConnection(connection.id);
  const isDrive = connection.provider === "google_drive";

  function handleRemove() {
    const confirmed = window.confirm(
      `Remove ${connection.name}? Its files stay in the account, and dextro forgets its credentials.`,
    );
    if (confirmed) {
      remove.mutate();
    }
  }

  return (
    <li className="space-y-2 py-4 first:pt-0">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{connection.name}</p>
          <p className="truncate text-sm text-muted-foreground">
            {isDrive ? `Google Drive, ${connection.summary}` : connection.summary}
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
      {test.isSuccess && <p className="text-sm">This connection works.</p>}
      <FormError
        message={
          test.error && isDrive
            ? "This account couldn't be reached. Use Connect Google Drive to repair it."
            : test.error?.message
        }
      />
      <FormError message={remove.error?.message} />
    </li>
  );
}

function ConnectionList() {
  const { data: connections, isPending, isError } = useQuery(connectionsQuery);

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Loading your connections…</p>;
  }
  if (isError) {
    return <FormError message="Your connections couldn't be loaded. Reload the page to try again." />;
  }
  if (connections.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Nothing is connected yet. Add a Google Drive account or an S3 bucket below to store files in it.
      </p>
    );
  }
  return (
    <ul className="divide-y">
      {connections.map((connection) => (
        <ConnectionRow key={connection.id} connection={connection} />
      ))}
    </ul>
  );
}

function GoogleDriveSection() {
  const connect = useConnectGoogleDrive();
  return (
    <section className="space-y-3 border-t pt-6">
      <h2 className="font-medium">Google Drive</h2>
      <p className="text-sm text-muted-foreground">
        dextro keeps its files in a folder named dextro and can't see anything else in your Drive.
      </p>
      <FormError message={connect.error?.message} />
      <Button variant="outline" disabled={connect.isPending} onClick={() => connect.mutate()}>
        Connect Google Drive
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
  const message = redirectError
    ? (REDIRECT_MESSAGES[redirectError] ?? REDIRECT_MESSAGES.google_failed)
    : undefined;

  return (
    <div className="max-w-2xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Storage</h1>
      <FormError message={message} />
      {connected === "google" && <p className="text-sm">Google Drive connected.</p>}
      <ConnectionList />
      {config?.google_enabled && <GoogleDriveSection />}
      <section className="space-y-3 border-t pt-6">
        <h2 className="font-medium">S3 bucket</h2>
        <p className="text-sm text-muted-foreground">
          Works with Amazon S3 and other S3-compatible services. The details are tried before they're
          saved.
        </p>
        <S3Form />
      </section>
    </div>
  );
}
