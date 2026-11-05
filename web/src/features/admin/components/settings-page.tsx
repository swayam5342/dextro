import { useQuery } from "@tanstack/react-query";

import { Label } from "@/components/ui/label";

import { settingsQuery, useUpdateSettings } from "../queries";

export function SettingsPage() {
  const { data: settings, isPending, isError } = useQuery(settingsQuery);
  const update = useUpdateSettings();

  return (
    <div className="max-w-xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Server settings</h1>
      {isPending && <p className="text-sm text-muted-foreground">Loading settings…</p>}
      {isError && (
        <p role="alert" className="text-sm text-destructive">
          Settings couldn't be loaded. Reload the page to try again.
        </p>
      )}
      {settings && (
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
            {update.error && (
              <p role="alert" className="text-sm text-destructive">
                {update.error.message}
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
