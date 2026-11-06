const SORT_KEY = "dextro.sort";

export const SORTS = [
  { id: "name-asc", label: "Name, A to Z" },
  { id: "name-desc", label: "Name, Z to A" },
  { id: "date-desc", label: "Newest first" },
  { id: "date-asc", label: "Oldest first" },
  { id: "size-desc", label: "Largest first" },
  { id: "size-asc", label: "Smallest first" },
] as const;

export type SortId = (typeof SORTS)[number]["id"];
export type SortField = "name" | "date" | "size";

interface Sortable {
  name: string;
  created_at: string;
  size?: number;
}

/** Numbers in names count as numbers, so "Day 2" comes before "Day 10". */
const names = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/** A remembered convenience, so it does without storage when the browser refuses it. */
export function loadSort(): SortId {
  try {
    const saved = localStorage.getItem(SORT_KEY);
    return SORTS.find((sort) => sort.id === saved)?.id ?? "name-asc";
  } catch {
    return "name-asc";
  }
}

export function saveSort(sort: SortId) {
  try {
    localStorage.setItem(SORT_KEY, sort);
  } catch {
    // It just won't be remembered.
  }
}

/** What a column heading does: flips its own sort, or starts with the useful end. */
export function nextSort(current: SortId, field: SortField): SortId {
  if (field === "name") {
    return current === "name-asc" ? "name-desc" : "name-asc";
  }
  return current === `${field}-desc` ? `${field}-asc` : `${field}-desc`;
}

/** Folders have no size, so sorting by size leaves them in name order. */
export function sorted<T extends Sortable>(items: T[], sort: SortId): T[] {
  const direction = sort.endsWith("asc") ? 1 : -1;
  const measure = (a: T, b: T) => {
    if (sort.startsWith("date")) {
      return Date.parse(a.created_at) - Date.parse(b.created_at);
    }
    if (sort.startsWith("size")) {
      return (a.size ?? 0) - (b.size ?? 0);
    }
    return names.compare(a.name, b.name);
  };
  return [...items].sort((a, b) => direction * measure(a, b) || names.compare(a.name, b.name));
}
