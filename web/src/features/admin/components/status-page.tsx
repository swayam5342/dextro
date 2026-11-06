import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Badge } from "@/components/ui/badge";
import { formatBytes, formatDateTime } from "@/lib/format";

import { healthQuery, statusQuery } from "../queries";

type JobRun = components["schemas"]["JobRun"];

const JOBS = [
  { name: "file_sync", label: "File sync", about: "Matches dextro to each account every 5 minutes." },
  { name: "file_transfers", label: "File moves", about: "Moves files between accounts, checking every 30 seconds." },
  { name: "quota_sync", label: "Quota sync", about: "Checks every linked account each hour." },
  { name: "session_cleanup", label: "Session cleanup", about: "Clears expired sign-ins each day." },
  { name: "upload_cleanup", label: "Upload cleanup", about: "Clears uploads left idle for a day, each hour." },
];

function Row({ title, about, children }: { title: string; about: string; children: ReactNode }) {
  return (
    <li className="flex items-center justify-between gap-4 border-t px-4 py-3 first:border-t-0">
      <div className="min-w-0">
        <p className="font-medium">{title}</p>
        <p className="text-sm text-muted-foreground">{about}</p>
      </div>
      {children}
    </li>
  );
}

function DatabaseBadge() {
  const { data, isPending, isError } = useQuery(healthQuery);
  if (isPending) {
    return <Badge variant="secondary">Checking…</Badge>;
  }
  if (isError || data.status !== "ok") {
    return <Badge variant="destructive">Unavailable</Badge>;
  }
  return <Badge>Healthy</Badge>;
}

function JobRow({ label, about, run }: { label: string; about: string; run: JobRun | undefined }) {
  const when = run ? `Last ran ${formatDateTime(run.finished_at)}` : about;
  return (
    <Row title={label} about={when}>
      {!run && <Badge variant="secondary">Hasn't run yet</Badge>}
      {run && (run.succeeded ? <Badge>Working</Badge> : <Badge variant="destructive">Failed</Badge>)}
    </Row>
  );
}

export function StatusPage() {
  const { data: status, isPending, isError } = useQuery(statusQuery);
  const accounts = status?.accounts === 1 ? "1 account" : `${status?.accounts} accounts`;

  return (
    <div className="max-w-2xl space-y-[18px]">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Server status</h1>
        {status && (
          <p className="text-muted-foreground">
            {formatBytes(status.used_bytes)} used across {accounts}
          </p>
        )}
      </div>
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && <FormError message="Status couldn't be loaded. Reload the page to try again." />}
      <ul className="rounded-2xl bg-card">
        <Row title="Database" about="Whether this server can reach its database.">
          <DatabaseBadge />
        </Row>
        {status &&
          JOBS.map((job) => (
            <JobRow
              key={job.name}
              label={job.label}
              about={job.about}
              run={status.jobs.find((run) => run.name === job.name)}
            />
          ))}
      </ul>
      {status && (
        <section className="space-y-2">
          <h2 className="font-medium">Accounts that can't be reached</h2>
          {status.unreachable_accounts.length === 0 ? (
            <p className="text-sm text-muted-foreground">Every linked account could be reached at the last check.</p>
          ) : (
            <>
              <p className="text-sm text-muted-foreground">
                Uploads skip these. Their owner can link them again from Storage.
              </p>
              <ul className="rounded-2xl bg-card">
                {status.unreachable_accounts.map((account) => (
                  <li key={`${account.owner_email}:${account.summary}`} className="border-t px-4 py-3 first:border-t-0">
                    <p className="truncate font-medium">{account.summary}</p>
                    <p className="truncate text-sm text-muted-foreground">
                      Linked by {account.owner_email}, checked {formatDateTime(account.checked_at)}
                    </p>
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}
    </div>
  );
}
