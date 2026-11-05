import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { formatDate } from "@/lib/format";

import { apiKeysQuery, useCreateAPIKey, useRevokeAPIKey } from "../queries";

type APIKey = components["schemas"]["APIKey"];
type Scope = APIKey["scopes"][number];

const SCOPE_LABELS: Record<Scope, string> = {
  "files:read": "Read files",
  "files:write": "Upload and change files",
};

function KeyRow({ apiKey }: { apiKey: APIKey }) {
  const revoke = useRevokeAPIKey();
  const [confirming, setConfirming] = useState(false);
  const used = apiKey.last_used_at ? `last used ${formatDate(apiKey.last_used_at)}` : "never used";

  return (
    <li className="space-y-2 py-3 first:pt-0">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{apiKey.name}</p>
          <p className="truncate text-sm text-muted-foreground">
            {apiKey.prefix}…, {apiKey.scopes.map((s) => SCOPE_LABELS[s]).join(" and ")}, {used}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setConfirming(true)}>
          Revoke
        </Button>
      </div>
      {confirming && (
        <ConfirmDialog
          title={`Revoke ${apiKey.name}?`}
          text="Anything using this key stops working right away."
          action="Revoke key"
          pending={revoke.isPending}
          error={revoke.error?.message}
          onConfirm={() => revoke.mutate(apiKey.id, { onSuccess: () => setConfirming(false) })}
          onClose={() => {
            setConfirming(false);
            revoke.reset();
          }}
        />
      )}
    </li>
  );
}

function NewKey({ token, onDone }: { token: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(token);
    setCopied(true);
  }

  return (
    <div className="space-y-3 rounded-2xl bg-card p-4">
      <p className="text-sm font-medium">Copy this key now. It won't be shown again.</p>
      <code className="block overflow-x-auto rounded-lg bg-raised p-2 text-sm">{token}</code>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => void handleCopy()}>
          {copied ? "Copied" : "Copy key"}
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}

export function APIKeysSection() {
  const { data: keys, isPending, isError } = useQuery(apiKeysQuery);
  const create = useCreateAPIKey();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<Scope[]>(["files:read"]);
  const [token, setToken] = useState<string | null>(null);

  function toggle(scope: Scope, on: boolean) {
    setScopes((current) => (on ? [...current, scope] : current.filter((s) => s !== scope)));
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    create.mutate(
      { name, scopes },
      {
        onSuccess: (created) => {
          setToken(created.token);
          setName("");
        },
      },
    );
  }

  return (
    <>
      <p className="text-sm text-muted-foreground">
        Keys let scripts work with your files. They can move files to the trash, but they can't delete
        anything forever or change your account.
      </p>
      {isPending && <p className="text-sm text-muted-foreground">Loading your keys…</p>}
      {isError && <FormError message="Your keys couldn't be loaded. Reload the page to try again." />}
      {keys && keys.length > 0 && (
        <ul className="divide-y">
          {keys.map((apiKey) => (
            <KeyRow key={apiKey.id} apiKey={apiKey} />
          ))}
        </ul>
      )}
      {token && <NewKey token={token} onDone={() => setToken(null)} />}
      <form onSubmit={handleSubmit} className="max-w-sm space-y-4">
        <Field
          id="api-key-name"
          label="Key name"
          hint="What will use this key."
          required
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Permissions</legend>
          {(Object.keys(SCOPE_LABELS) as Scope[]).map((scope) => (
            <label key={scope} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={scopes.includes(scope)}
                onChange={(event) => toggle(scope, event.target.checked)}
              />
              {SCOPE_LABELS[scope]}
            </label>
          ))}
        </fieldset>
        <FormError message={create.error?.message} />
        <Button type="submit" variant="outline" disabled={create.isPending}>
          Create key
        </Button>
      </form>
    </>
  );
}
