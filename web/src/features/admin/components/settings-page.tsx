import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";

import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";

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
    <form onSubmit={handleSubmit} className="max-w-sm space-y-4">
      <Field
        id="max-upload"
        label="Largest upload, in GiB"
        type="number"
        min="0.001"
        step="any"
        required
        value={gib}
        onChange={(event) => setGib(event.target.value)}
      />
      <FormError message={update.error?.message} />
      {update.isSuccess && <p className="text-sm">Upload limit saved.</p>}
      <Button type="submit" variant="outline" disabled={update.isPending}>
        Save upload limit
      </Button>
    </form>
  );
}

export function SettingsPage() {
  const { data: settings, isPending, isError } = useQuery(settingsQuery);
  const update = useUpdateSettings();

  return (
    <div className="max-w-xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Server settings</h1>
      {isPending && <p className="text-sm text-muted-foreground">Loading settings…</p>}
      {isError && <FormError message="Settings couldn't be loaded. Reload the page to try again." />}
      {settings && (
        <>
          <div className="flex items-start gap-3">
            <input
              id="allow-registration"
              type="checkbox"
              className="mt-0.5 size-4 accent-primary"
              aria-describedby="allow-registration-hint"
              checked={settings.allow_registration}
              disabled={update.isPending}
              onChange={(event) => update.mutate({ allow_registration: event.target.checked })}
            />
            <div className="space-y-1">
              <Label htmlFor="allow-registration">Allow new accounts</Label>
              <p id="allow-registration-hint" className="text-sm text-muted-foreground">
                When this is off, nobody can create an account, with a password or with Google. People
                who already have an account can still sign in.
              </p>
              <FormError message={update.error?.message} />
            </div>
          </div>
          <UploadLimit current={settings.max_upload_bytes} />
        </>
      )}
      <p className="text-sm">
        <Link to="/status" className="underline underline-offset-4">
          Server status
        </Link>
      </p>
    </div>
  );
}
