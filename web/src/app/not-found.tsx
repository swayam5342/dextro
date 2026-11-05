import { Link } from "@tanstack/react-router";

export function NotFound() {
  return (
    <div className="space-y-2">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <p className="text-muted-foreground">
        This page doesn't exist.{" "}
        <Link to="/" className="underline underline-offset-4">
          Go back home
        </Link>
      </p>
    </div>
  );
}
