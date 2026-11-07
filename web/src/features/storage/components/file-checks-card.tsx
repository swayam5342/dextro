import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { fileChecksQuery, useDismissProblem } from "@/features/files/queries";
import { formatBytes, formatDate } from "@/lib/format";

type Pool = components["schemas"]["Pool"];
type FileProblem = components["schemas"]["FileProblem"];

function problemText(problem: FileProblem, account: string): string {
  switch (problem.kind) {
    case "missing":
      return `Gone from ${account}, so it's no longer in Tributary.`;
    case "changed":
      return `Changed in ${account} since Tributary stored it. Dismiss this if the change was yours.`;
    case "damaged":
      return `What ${account} sent back doesn't match what it stored. Replace this file from your own copy if you can.`;
  }
}

/** What the background checks have found, because a lost file is easy to miss. */
export function FileChecksCard({ pool }: { pool: Pool | undefined }) {
  const { data } = useQuery(fileChecksQuery);
  const dismiss = useDismissProblem();
  if (!data) {
    return null;
  }

  const account = (id: string) => pool?.accounts.find((item) => item.connection.id === id)?.connection.summary ?? "its account";

  return (
    <section aria-labelledby="file-checks" className="space-y-2.5 rounded-[14px] bg-card p-3.5">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <h2 id="file-checks" className="text-base font-semibold">
          File checks
        </h2>
        <Link to="/duplicates" className="text-sm text-muted-foreground hover:text-foreground">
          Find duplicates
        </Link>
      </div>
      <p className="text-sm text-muted-foreground">
        {data.enabled
          ? `Tributary reads your files back in the background, about 1 GB an hour. ${data.checked_files} of ${data.checkable_files} have been checked so far.`
          : "Reading files back is turned off in server settings. Files that go missing or change in Drive are still noticed."}
      </p>
      <FormError message={dismiss.error?.message} />
      {data.problems.length > 0 && (
        <ul className="divide-y">
          {data.problems.map((problem) => (
            <li key={problem.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 py-2">
              <span className="min-w-0 flex-1 basis-56">
                <span className="block truncate font-medium">{problem.name}</span>
                <span className="block text-[13px] text-muted-foreground">{problemText(problem, account(problem.connection_id))}</span>
                <span className="block text-xs text-faint">
                  {formatBytes(problem.size)}, found {formatDate(problem.found_at)}
                </span>
              </span>
              <Button
                variant="soft"
                size="sm"
                disabled={dismiss.isPending}
                aria-label={`Dismiss the problem with ${problem.name}`}
                onClick={() => dismiss.mutate(problem.id)}
              >
                Dismiss
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
