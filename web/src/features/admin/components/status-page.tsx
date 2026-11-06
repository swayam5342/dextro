import { useQuery } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";

import { failureMessage } from "@/api/errors";
import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { useToast } from "@/components/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDateTime } from "@/lib/format";

import { healthQuery, statusQuery, useRunJob } from "../queries";

type JobRun = components["schemas"]["JobRun"];

const JOBS = [
  { name: "file_sync", label: "File sync", about: "Matches Tributary to each account every 5 minutes." },
  { name: "file_transfers", label: "File moves", about: "Moves files between accounts, checking every 30 seconds." },
  { name: "share_expiry", label: "Link expiry", about: "Ends Google links past their expiry, each minute." },
  { name: "quota_sync", label: "Quota sync", about: "Checks every linked account each hour." },
  { name: "session_cleanup", label: "Session cleanup", about: "Clears expired sign-ins each day." },
  { name: "upload_cleanup", label: "Upload cleanup", about: "Clears uploads left idle for a day, each hour." },
];

/** A started job is running until its last finish moves on. */
function isBusy(name: string, started: Record<string, string | undefined>, jobs: JobRun[] | undefined): boolean {
  if (!(name in started)) {
    return false;
  }
  return (jobs?.find((run) => run.name === name)?.finished_at ?? "") === started[name];
}

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

function JobRow({
  label,
  about,
  run,
  busy,
  onRun,
}: {
  label: string;
  about: string;
  run: JobRun | undefined;
  busy: boolean;
  onRun: () => void;
}) {
  const when = run ? `Last ran ${formatDateTime(run.finished_at)}` : about;
  return (
    <Row title={label} about={when}>
      <div className="flex shrink-0 items-center gap-2">
        {!run && <Badge variant="secondary">Hasn't run yet</Badge>}
        {run && (run.succeeded ? <Badge>Working</Badge> : <Badge variant="destructive">Failed</Badge>)}
        <Button variant="soft" size="sm" disabled={busy} aria-label={`Run ${label} now`} onClick={onRun}>
          {busy ? "Running…" : "Run now"}
        </Button>
      </div>
    </Row>
  );
}

export function StatusPage() {
  // Each started job, and when it last finished at that moment. It's done when that changes.
  const [started, setStarted] = useState<Record<string, string | undefined>>({});
  const runJob = useRunJob();
  const toast = useToast();
  const { data: status, isPending, isError } = useQuery({
    ...statusQuery,
    refetchInterval: (query) => {
      const jobs = query.state.data?.jobs;
      return JOBS.some((job) => isBusy(job.name, started, jobs)) ? 1500 : false;
    },
  });
  const accounts = status?.accounts === 1 ? "1 account" : `${status?.accounts} accounts`;

  function start(job: (typeof JOBS)[number]) {
    const before = status?.jobs.find((run) => run.name === job.name)?.finished_at;
    runJob.mutate(job.name, {
      onSuccess: () => setStarted((current) => ({ ...current, [job.name]: before ?? "" })),
      onError: (error) => toast({ message: failureMessage(error) }),
    });
  }

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
              busy={isBusy(job.name, started, status.jobs)}
              onRun={() => start(job)}
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
