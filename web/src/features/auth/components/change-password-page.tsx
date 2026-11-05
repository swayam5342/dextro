import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";

import { CenteredPage } from "@/app/centered-page";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";

import { useChangePassword, useLogout } from "../queries";

/** Where someone stays until they've replaced a password the admin chose. */
export function ChangePasswordPage() {
  const navigate = useNavigate();
  const change = useChangePassword();
  const logout = useLogout();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    change.mutate(
      { current_password: current, new_password: next },
      { onSuccess: () => navigate({ to: "/", search: {} }) },
    );
  }

  return (
    <CenteredPage>
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-tight">Choose your own password</h1>
        <p className="text-muted-foreground">
          Your admin set the password you just used, so it needs replacing before you can continue.
        </p>
      </div>
      <form onSubmit={handleSubmit} className="space-y-4">
        <Field
          id="current-password"
          label="Password from your admin"
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
          hint="At least 12 characters."
          required
          value={next}
          onChange={(event) => setNext(event.target.value)}
        />
        <FormError message={change.error?.message} />
        <Button type="submit" className="w-full" disabled={change.isPending}>
          {change.isPending ? "Changing password…" : "Change password"}
        </Button>
      </form>
      <Button
        variant="ghost"
        className="w-full"
        onClick={() => logout.mutate(undefined, { onSuccess: () => navigate({ to: "/sign-in" }) })}
      >
        Sign out
      </Button>
    </CenteredPage>
  );
}
