import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";

import { settingsQuery, useUpdateSettings } from "../queries";

const BYTES_PER_GIB = 1024 ** 3;

function UploadLimit({ current }: { current: number }) {
  const [gib, setGib] = useState(String(current / BYTES_PER_GIB));
  const update = useUpdateSettings();

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    update.mutate({ max_upload_bytes: Math.round(Number(gib) * BYTES_PER_GIB) });
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4 rounded-2xl bg-card p-4">
      <Field
        id="max-upload"
        label="Largest upload, in GiB"
        className="max-w-48 bg-raised"
        type="number"
        min="0.001"
        step="any"
        required
        value={gib}
        onChange={(event) => setGib(event.target.value)}
      />
      <FormError message={update.error?.message} />
      {update.isSuccess && <p className="text-sm">Upload limit saved.</p>}
      <Button type="submit" variant="soft" disabled={update.isPending}>
        Save upload limit
      </Button>
    </form>
  );
}

function GoogleLinks({ enabled }: { enabled: boolean }) {
  const update = useUpdateSettings();
  return (
    <section aria-label="Google links" className="space-y-3 rounded-2xl bg-card p-4">
      <div>
        <h2 className="font-semibold">Google links are {enabled ? "on" : "off"}</h2>
        <p className="text-sm text-muted-foreground">
          People can make a public Google link for a file, which anyone who has it can open without signing in. Turning
          this off stops new links, and links that exist keep working until their owner removes them.
        </p>
      </div>
      <FormError message={update.error?.message} />
      <Button
        variant="soft"
        disabled={update.isPending}
        onClick={() => update.mutate({ google_links_enabled: !enabled })}
      >
        {enabled ? "Turn off Google links" : "Turn on Google links"}
      </Button>
    </section>
  );
}

function FileChecks({ enabled, hours, megabytes }: { enabled: boolean; hours: number; megabytes: number }) {
  const [everyHours, setEveryHours] = useState(String(hours));
  const [limit, setLimit] = useState(String(megabytes));
  const update = useUpdateSettings();
  const schedule = useUpdateSettings();

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    schedule.mutate({ file_check_every_hours: Number(everyHours), file_check_limit_mb: Number(limit) });
  }

  return (
    <section aria-label="File checks" className="space-y-3 rounded-2xl bg-card p-4">
      <div>
        <h2 className="font-semibold">File checks are {enabled ? "on" : "off"}</h2>
        <p className="text-sm text-muted-foreground">
          The server downloads some of your files now and then to confirm they're intact, which uses your internet
          allowance. Files bigger than the limit aren't checked. Turning this off stops those downloads. Files that go
          missing or change in Drive are still noticed, but damage inside a file isn't.
        </p>
      </div>
      <form onSubmit={handleSubmit} className="space-y-3">
        <div className="flex flex-wrap gap-3">
          <Field
            id="file-check-hours"
            label="Check every, in hours"
            className="max-w-36 bg-raised"
            type="number"
            min="1"
            max="720"
            step="1"
            required
            value={everyHours}
            onChange={(event) => setEveryHours(event.target.value)}
          />
          <Field
            id="file-check-limit"
            label="Read up to, in MB"
            className="max-w-36 bg-raised"
            type="number"
            min="1"
            max="1000000"
            step="1"
            required
            value={limit}
            onChange={(event) => setLimit(event.target.value)}
          />
        </div>
        <FormError message={schedule.error?.message} />
        {schedule.isSuccess && <p className="text-sm">Schedule saved.</p>}
        <Button type="submit" variant="soft" disabled={schedule.isPending}>
          Save schedule
        </Button>
      </form>
      <FormError message={update.error?.message} />
      <Button
        variant="soft"
        disabled={update.isPending}
        onClick={() => update.mutate({ file_checks_enabled: !enabled })}
      >
        {enabled ? "Turn off file checks" : "Turn on file checks"}
      </Button>
    </section>
  );
}

export function SettingsPage() {
  const { data: settings, isPending, isError } = useQuery(settingsQuery);

  return (
    <div className="max-w-xl space-y-4">
      <h1 className="text-2xl font-semibold tracking-tight">Server settings</h1>
      {isPending && <p className="text-sm text-muted-foreground">Loading settings…</p>}
      {isError && <FormError message="Settings couldn't be loaded. Reload the page to try again." />}
      {settings && <UploadLimit current={settings.max_upload_bytes} />}
      {settings && <GoogleLinks enabled={settings.google_links_enabled} />}
      {settings && (
        <FileChecks
          enabled={settings.file_checks_enabled}
          hours={settings.file_check_every_hours}
          megabytes={settings.file_check_limit_mb}
        />
      )}
    </div>
  );
}
