import { createRoute, redirect } from "@tanstack/react-router";

import { rootRoute } from "@/app/root-route";

import { AccountPage } from "./components/account-page";
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
  beforeLoad: async ({ context }) => {
    const user = await context.queryClient.fetchQuery(meQuery);
    if (!user) {
      throw redirect({ to: "/sign-in" });
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
