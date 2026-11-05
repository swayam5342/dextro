import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { describeDevice } from "../device";
import { redirectMessage } from "../messages";
import {
  authConfigQuery,
  meQuery,
  sessionsQuery,
  useChangePassword,
  useLinkGoogle,
  useRevokeSession,
} from "../queries";
import { Field, FormError } from "@/components/field";

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="grid gap-4 border-t py-6 md:grid-cols-[12rem_1fr]">
      <h2 className="font-medium">{title}</h2>
      <div className="min-w-0 space-y-4">{children}</div>
    </section>
  );
}

function PasswordForm() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const change = useChangePassword();

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    change.mutate(
      { current_password: current, new_password: next },
      {
        onSuccess: () => {
          setCurrent("");
          setNext("");
        },
      },
    );
  }

  return (
    <form onSubmit={handleSubmit} className="max-w-sm space-y-4">
      <Field
        id="current-password"
        label="Current password"
        type="password"
        autoComplete="current-password"
        required
        value={current}
        onChange={(event) => setCurrent(event.target.value)}
      />
      <Field
        id="new-password"
        label="New password"
        type="password"
        autoComplete="new-password"
        hint="At least 12 characters. Changing it signs out your other devices."
        required
        value={next}
        onChange={(event) => setNext(event.target.value)}
      />
      <FormError message={change.error?.message} />
      {change.isSuccess && <p className="text-sm">Password changed.</p>}
      <Button type="submit" disabled={change.isPending}>
        {change.isPending ? "Changing password…" : "Change password"}
      </Button>
    </form>
  );
}

function GoogleSection({ linked, justLinked }: { linked: boolean; justLinked: boolean }) {
  const link = useLinkGoogle();
  if (linked) {
    return (
      <p className="text-sm text-muted-foreground">
        {justLinked ? "Google linked. You can now use it to sign in." : "You can sign in with Google."}
      </p>
    );
  }
  return (
    <>
      <p className="text-sm text-muted-foreground">Link a Google account to use it for signing in.</p>
      <FormError message={link.error?.message} />
      <Button variant="outline" disabled={link.isPending} onClick={() => link.mutate()}>
        Link Google account
      </Button>
    </>
  );
}

function Sessions() {
  const { data: sessions, isPending, isError } = useQuery(sessionsQuery);
  const revoke = useRevokeSession();

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Loading your devices…</p>;
  }
  if (isError) {
    return <FormError message="Your devices couldn't be loaded. Reload the page to try again." />;
  }
  return (
    <>
      <FormError message={revoke.error?.message} />
      <ul className="divide-y">
        {sessions.map((session) => (
          <li key={session.id} className="flex items-center justify-between gap-4 py-3 first:pt-0">
            <div className="min-w-0">
              <p className="flex items-center gap-2 text-sm font-medium">
                {describeDevice(session.user_agent)}
                {session.current && <Badge variant="secondary">This device</Badge>}
              </p>
              <p className="truncate text-sm text-muted-foreground">
                {session.ip}, last active {new Date(session.last_seen_at).toLocaleString()}
              </p>
            </div>
            {!session.current && (
              <Button
                variant="outline"
                size="sm"
                disabled={revoke.isPending}
                onClick={() => revoke.mutate(session.id)}
              >
                Sign out
              </Button>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

export function AccountPage({
  redirectError,
  linked,
}: {
  redirectError: string | undefined;
  linked: string | undefined;
}) {
  const { data: user } = useQuery(meQuery);
  const { data: config } = useQuery(authConfigQuery);
  if (!user) {
    return null;
  }

  return (
    <div className="max-w-3xl space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Your account</h1>
        <p className="text-muted-foreground">{user.email}</p>
      </div>
      <FormError message={redirectMessage(redirectError)} />
      <div className="border-b">
        {user.has_password && (
          <Section title="Password">
            <PasswordForm />
          </Section>
        )}
        {(config?.google_enabled || user.google_linked) && (
          <Section title="Google">
            <GoogleSection linked={user.google_linked} justLinked={linked === "google"} />
          </Section>
        )}
        <Section title="Devices">
          <Sessions />
        </Section>
      </div>
    </div>
  );
}
