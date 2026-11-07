import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Check, ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { connectionsQuery } from "@/features/storage/queries";

import { currentOperator, quoted, withOperator } from "../search-query";

interface Option {
  label: string;
  value: string;
}

interface Filter {
  label: string;
  keys: string[];
  options: Option[];
}

const FILTERS: Filter[] = [
  {
    label: "Type",
    keys: ["type"],
    options: [
      { label: "Photos", value: "type:image" },
      { label: "Videos", value: "type:video" },
      { label: "Audio", value: "type:audio" },
      { label: "Documents", value: "type:document" },
      { label: "Spreadsheets", value: "type:spreadsheet" },
      { label: "Archives", value: "type:archive" },
    ],
  },
  {
    label: "Size",
    keys: ["size", "larger", "smaller"],
    options: [
      { label: "Under 1 MB", value: "smaller:1mb" },
      { label: "Over 10 MB", value: "larger:10mb" },
      { label: "Over 100 MB", value: "larger:100mb" },
      { label: "Over 1 GB", value: "larger:1gb" },
    ],
  },
  {
    label: "Date added",
    keys: ["after", "before", "newer_than", "older_than"],
    options: [
      { label: "Last 7 days", value: "newer_than:7d" },
      { label: "Last 30 days", value: "newer_than:30d" },
      { label: "Last year", value: "newer_than:1y" },
      { label: "Over a year ago", value: "older_than:1y" },
    ],
  },
];

function FilterMenu({ filter, query, onChange }: { filter: Filter; query: string; onChange: (next: string) => void }) {
  const current = currentOperator(query, filter.keys);
  const chosen = filter.options.find((option) => option.value === current);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant={current ? "soft" : "outline"} size="sm">
          {chosen?.label ?? current ?? filter.label}
          <ChevronDown />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem onSelect={() => onChange(withOperator(query, filter.keys, null))}>
          <span className="flex-1">Any</span>
          {!current && <Check />}
        </DropdownMenuItem>
        {filter.options.map((option) => (
          <DropdownMenuItem key={option.value} onSelect={() => onChange(withOperator(query, filter.keys, option.value))}>
            <span className="flex-1">{option.label}</span>
            {option.value === current && <Check />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Filters for a search. They only edit the text in the search box, so what they do can be
 * seen, typed and shared, the way Gmail's chips work.
 */
export function SearchFilters({ query }: { query: string }) {
  const navigate = useNavigate();
  const { data: connections } = useQuery(connectionsQuery);

  function change(next: string) {
    void navigate({ to: "/", search: next ? { q: next } : {}, replace: true });
  }

  const accounts: Filter = {
    label: "Account",
    keys: ["account", "owner"],
    options: (connections ?? []).map((connection) => ({
      label: connection.summary,
      value: `account:${quoted(connection.summary)}`,
    })),
  };

  return (
    <div className="mb-[18px] space-y-1.5">
      <div role="group" aria-label="Filter results" className="flex flex-wrap gap-2">
        {[...FILTERS, accounts].map((filter) => (
          <FilterMenu key={filter.label} filter={filter} query={query} onChange={change} />
        ))}
      </div>
      <p className="text-xs text-faint">
        Or type them: <code>type:image</code> <code>size:&gt;10mb</code> <code>after:2026-01-31</code> <code>older_than:1y</code>{" "}
        <code>owner:name</code> <code>in:folder</code> <code>is:starred</code> <code>-word</code>
      </p>
    </div>
  );
}
