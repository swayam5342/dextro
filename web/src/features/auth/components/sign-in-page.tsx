import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";

import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";

import { redirectMessage } from "../messages";
import { authConfigQuery, useLogin } from "../queries";
import { CredentialsForm } from "./credentials-form";

export function SignInPage({ redirectError }: { redirectError: string | undefined }) {
  const navigate = useNavigate();
  const login = useLogin();
  const { data: config } = useQuery(authConfigQuery);

  return (
    <div className="max-w-sm space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Sign in to dextro</h1>
      <FormError message={redirectMessage(redirectError)} />
      <CredentialsForm
        submitLabel="Sign in"
        pendingLabel="Signing in…"
        passwordAutoComplete="current-password"
        pending={login.isPending}
        error={login.error?.message}
        onSubmit={(credentials) =>
          login.mutate(credentials, { onSuccess: () => navigate({ to: "/" }) })
        }
      />
      {config?.google_enabled && (
        <Button asChild variant="outline" className="w-full">
          {/* A full page load: the server redirects this to Google. */}
          <a href="/api/v1/auth/google/start">Continue with Google</a>
        </Button>
      )}
      {config?.registration_open && (
        <p className="text-sm text-muted-foreground">
          New here?{" "}
          <Link to="/register" className="text-foreground underline underline-offset-4">
            Create an account
          </Link>
        </p>
      )}
    </div>
  );
}
