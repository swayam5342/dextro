// Order matters: Edge's user agent also says Chrome, and Chrome's also says Safari.
const BROWSERS: [RegExp, string][] = [
  [/Edg\//, "Edge"],
  [/Firefox\//, "Firefox"],
  [/Chrome\//, "Chrome"],
  [/Safari\//, "Safari"],
];

// Android's user agent also says Linux, and the iPhone's also says Mac OS X.
const SYSTEMS: [RegExp, string][] = [
  [/Android/, "Android"],
  [/iPhone|iPad/, "iOS"],
  [/Windows/, "Windows"],
  [/Mac OS X/, "macOS"],
  [/Linux/, "Linux"],
];

function firstMatch(table: [RegExp, string][], userAgent: string): string | undefined {
  return table.find(([pattern]) => pattern.test(userAgent))?.[1];
}

/** Turns a user agent into something a person recognizes, like "Firefox on Windows". */
export function describeDevice(userAgent: string): string {
  const browser = firstMatch(BROWSERS, userAgent);
  const system = firstMatch(SYSTEMS, userAgent);
  if (browser && system) {
    return `${browser} on ${system}`;
  }
  return browser ?? system ?? "Unknown device";
}
