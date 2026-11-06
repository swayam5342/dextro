import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";

import { CenteredPage } from "@/app/centered-page";
import { Button } from "@/components/ui/button";

import { authConfigQuery, useRegister } from "../queries";
import { CredentialsForm } from "./credentials-form";

export function RegisterPage() {
  const navigate = useNavigate();
  const register = useRegister();
  const { data: config } = useQuery(authConfigQuery);

  if (config && !config.registration_open) {
    return (
      <CenteredPage>
        <div className="space-y-2">
          <h1 className="text-2xl font-semibold tracking-tight">Ask your admin for an account</h1>
          <p className="text-muted-foreground">
            This server doesn't take sign-ups. Its admin creates accounts, or you can{" "}
            <Link to="/sign-in" className="text-foreground underline underline-offset-4">
              sign in
            </Link>{" "}
            if you already have an account.
          </p>
        </div>
      </CenteredPage>
    );
  }

  return (
    <CenteredPage>
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Set up this server</h1>
        <p className="text-muted-foreground">The first account becomes the admin. Everyone else gets an account from you.</p>
      </div>
      <CredentialsForm
        submitLabel="Create admin account"
        pendingLabel="Creating account…"
        passwordAutoComplete="new-password"
        passwordHint="At least 12 characters."
        pending={register.isPending}
        error={register.error?.message}
        onSubmit={(credentials) =>
          register.mutate(credentials, { onSuccess: () => navigate({ to: "/", search: {} }) })
        }
      />
      {config?.google_enabled && (
        <Button asChild variant="outline" className="w-full">
          <a href="/api/v1/auth/google/start">Continue with Google</a>
        </Button>
      )}
      <p className="text-sm text-muted-foreground">
        Already have an account?{" "}
        <Link to="/sign-in" className="text-foreground underline underline-offset-4">
          Sign in
        </Link>
      </p>
    </CenteredPage>
  );
}
