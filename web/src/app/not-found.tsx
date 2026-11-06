import { Link } from "@tanstack/react-router";

import { CenteredPage } from "./centered-page";

export function NotFound() {
  return (
    <CenteredPage>
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
        <p className="text-muted-foreground">
          This page doesn't exist.{" "}
          <Link to="/" search={{}} className="text-foreground underline underline-offset-4">
            Go to your files
          </Link>
        </p>
      </div>
    </CenteredPage>
  );
}
