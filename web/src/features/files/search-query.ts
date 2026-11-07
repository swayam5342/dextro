/** The pieces of a search, keeping anything in quotes together, like the server does. */
function tokens(query: string): string[] {
  return query.match(/(?:[^\s"]+|"[^"]*")+/g) ?? [];
}

function matches(token: string, keys: string[]): boolean {
  const lower = token.toLowerCase();
  return keys.some((key) => lower.startsWith(`${key}:`));
}

/** The part of a search using one of these operators, if there is one. */
export function currentOperator(query: string, keys: string[]): string | undefined {
  return tokens(query).find((token) => matches(token, keys));
}

/** Puts an operator in a search, replacing any it already had, or takes it out. */
export function withOperator(query: string, keys: string[], value: string | null): string {
  const kept = tokens(query).filter((token) => !matches(token, keys));
  return (value ? [...kept, value] : kept).join(" ");
}

/** Quotes a value that has spaces in it. */
export function quoted(value: string): string {
  return /\s/.test(value) ? `"${value}"` : value;
}
