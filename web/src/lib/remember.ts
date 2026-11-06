/** Per-browser conveniences. Storage can be refused, and the app has to work without it. */
export function recall(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function remember(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // It just won't be remembered.
  }
}
