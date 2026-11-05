import { Link, Outlet } from "@tanstack/react-router";

import { UserNav } from "@/features/auth/components/user-nav";

export function RootLayout() {
  return (
    <div className="flex min-h-svh flex-col">
      <header className="border-b">
        <div className="mx-auto flex min-h-14 max-w-5xl flex-wrap items-center justify-between gap-x-4 px-4 py-2">
          <Link to="/" className="font-semibold tracking-tight">
            dextro
          </Link>
          <UserNav />
        </div>
      </header>
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}
