import { useState, type FormEvent } from "react";

import { Button } from "@/components/ui/button";

import { Field, FormError } from "@/components/field";

interface CredentialsFormProps {
  submitLabel: string;
  pendingLabel: string;
  passwordAutoComplete: "current-password" | "new-password";
  passwordHint?: string;
  pending: boolean;
  error: string | undefined;
  onSubmit: (credentials: { email: string; password: string }) => void;
}

export function CredentialsForm({
  submitLabel,
  pendingLabel,
  passwordAutoComplete,
  passwordHint,
  pending,
  error,
  onSubmit,
}: CredentialsFormProps) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    onSubmit({ email, password });
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <Field
        id="email"
        label="Email"
        type="email"
        autoComplete="email"
        required
        value={email}
        onChange={(event) => setEmail(event.target.value)}
      />
      <Field
        id="password"
        label="Password"
        type="password"
        autoComplete={passwordAutoComplete}
        hint={passwordHint}
        required
        value={password}
        onChange={(event) => setPassword(event.target.value)}
      />
      <FormError message={error} />
      <Button type="submit" className="w-full" disabled={pending}>
        {pending ? pendingLabel : submitLabel}
      </Button>
    </form>
  );
}
