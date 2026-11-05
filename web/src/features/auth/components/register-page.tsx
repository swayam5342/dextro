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
          <h1 className="text-xl font-semibold tracking-tight">Registration is closed</h1>
          <p className="text-muted-foreground">
            This server isn't accepting new accounts. Ask its admin to open registration, or{" "}
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
      <h1 className="text-xl font-semibold tracking-tight">Create your account</h1>
      <CredentialsForm
        submitLabel="Create account"
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
