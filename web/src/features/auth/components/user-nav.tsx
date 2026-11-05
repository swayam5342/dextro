import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";

import { meQuery, useLogout } from "../queries";

export function UserNav() {
  const navigate = useNavigate();
  const { data: user } = useQuery(meQuery);
  const logout = useLogout();

  if (!user) {
    return null;
  }
  return (
    <nav className="flex flex-wrap items-center gap-1">
      <Button asChild variant="ghost" size="sm">
        <Link to="/" search={{}}>
          Files
        </Link>
      </Button>
      <Button asChild variant="ghost" size="sm">
        <Link to="/storage">Storage</Link>
      </Button>
      {user.role === "admin" && (
        <Button asChild variant="ghost" size="sm">
          <Link to="/admin/settings">Server settings</Link>
        </Button>
      )}
      <Button asChild variant="ghost" size="sm">
        <Link to="/account">Account</Link>
      </Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={logout.isPending}
        onClick={() => logout.mutate(undefined, { onSuccess: () => navigate({ to: "/sign-in" }) })}
      >
        Sign out
      </Button>
    </nav>
  );
}
