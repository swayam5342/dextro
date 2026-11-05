const REDIRECT_MESSAGES: Record<string, string> = {
  account_exists:
    "An account with this email already exists. Sign in with your password, then link Google from your account page.",
  registration_closed: "This server doesn't take sign-ups. Ask its admin for an account.",
  google_email_unverified: "Google hasn't verified this email address, so it can't be used here.",
  google_already_linked:
    "This Google account is linked to another account here, or this account already has one linked.",
  google_cancelled: "Google sign-in was cancelled.",
  google_failed: "Google sign-in didn't finish. Try again.",
  rate_limited: "Too many attempts. Wait a minute and try again.",
};

/** The message for an error code sent back in a redirect from Google sign-in. */
export function redirectMessage(code: string | undefined): string | undefined {
  if (!code) {
    return undefined;
  }
  return REDIRECT_MESSAGES[code] ?? REDIRECT_MESSAGES.google_failed;
}
