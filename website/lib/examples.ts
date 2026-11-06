import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Sample } from "@/components/code-tabs";

const LANGUAGES = [
  { label: "curl", extension: "sh" },
  { label: "Python", extension: "py" },
  { label: "JavaScript", extension: "mjs" },
  { label: "Go", extension: "go" },
];

/** The samples on the page are the same files that get run to test them. */
export function read(file: string): string {
  return readFileSync(join(process.cwd(), "_examples", file), "utf8").trimEnd();
}

export function samples(name: string): Sample[] {
  return LANGUAGES.map(({ label, extension }) => ({ label, code: read(`${name}.${extension}`) }));
}
