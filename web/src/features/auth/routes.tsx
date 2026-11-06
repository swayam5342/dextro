import { createRoute, redirect } from "@tanstack/react-router";

import { AppShell } from "@/app/app-shell";
import { rootRoute } from "@/app/root-route";

import { AccountPage } from "./components/account-page";
import { ChangePasswordPage } from "./components/change-password-page";
import { RegisterPage } from "./components/register-page";
import { SignInPage } from "./components/sign-in-page";
import { meQuery } from "./queries";

function textParam(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

/** Parent of every page that needs a signed-in user. */
export const authedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "authed",
  component: AppShell,
  beforeLoad: async ({ context }) => {
    const user = await context.queryClient.fetchQuery(meQuery);
    if (!user) {
      throw redirect({ to: "/sign-in" });
    }
    if (user.must_change_password) {
      throw redirect({ to: "/change-password" });
    }
    return { user };
  },
});

export const signInRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sign-in",
  validateSearch: (search): { error?: string } => ({ error: textParam(search.error) }),
  beforeLoad: async ({ context }) => {
    if (await context.queryClient.fetchQuery(meQuery)) {
      throw redirect({ to: "/" });
    }
  },
  component: function SignInRoute() {
    return <SignInPage redirectError={signInRoute.useSearch().error} />;
  },
});

/** Sends a common way of writing an address to the real page, whose rules then apply. */
function alias(path: string, to: "/sign-in" | "/register") {
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: () => {
      throw redirect({ to });
    },
  });
}

export const aliasRoutes = [
  alias("/login", "/sign-in"),
  alias("/signin", "/sign-in"),
  alias("/signup", "/register"),
  alias("/sign-up", "/register"),
];

export const registerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/register",
  beforeLoad: async ({ context }) => {
    if (await context.queryClient.fetchQuery(meQuery)) {
      throw redirect({ to: "/" });
    }
  },
  component: RegisterPage,
});

// Outside the frame, because the server refuses everything else until it's done.
export const changePasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/change-password",
  beforeLoad: async ({ context }) => {
    const user = await context.queryClient.fetchQuery(meQuery);
    if (!user) {
      throw redirect({ to: "/sign-in" });
    }
    if (!user.must_change_password) {
      throw redirect({ to: "/" });
    }
  },
  component: ChangePasswordPage,
});

export const accountRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/account",
  validateSearch: (search): { error?: string; linked?: string } => ({
    error: textParam(search.error),
    linked: textParam(search.linked),
  }),
  component: function AccountRoute() {
    const { error, linked } = accountRoute.useSearch();
    return <AccountPage redirectError={error} linked={linked} />;
  },
});
