import { useQuery } from "@tanstack/react-query";
import { Check } from "lucide-react";
import { useRef, useState } from "react";

import type { components } from "@/api/schema";
import { Choice } from "@/components/choice";
import { FormError } from "@/components/field";
import { useToast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { formatDate } from "@/lib/format";

import { sharesQuery, useRemoveShare, useSaveShare } from "../queries";

type Share = components["schemas"]["Share"];
type Role = Share["role"];

const DAY_MS = 86_400_000;

const ROLES: { id: Role; label: string; text: string }[] = [
  { id: "viewer", label: "Viewer", text: "Can see and download it." },
  { id: "commenter", label: "Commenter", text: "Can also leave comments." },
  { id: "editor", label: "Editor", text: "Can also change or replace the file in your Drive." },
];

const EXPIRIES = [
  { id: "never", label: "Never", days: null },
  { id: "1", label: "1 day", days: 1 },
  { id: "7", label: "7 days", days: 7 },
  { id: "30", label: "30 days", days: 30 },
];

/** Puts a link on the clipboard, or selects it for copying where that's not allowed. */
export function CopyButton({ text, fallback, label }: { text: string; fallback?: () => void; label: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      fallback?.();
    }
  }

  return (
    <Button variant="soft" size="sm" aria-label={label} onClick={() => void copy()}>
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}

/** Making a file public is the most serious thing the app does, so this says so plainly. */
export function ShareDialog({ fileId, name, onClose }: { fileId: string; name: string; onClose: () => void }) {
  const { data } = useQuery(sharesQuery);
  const save = useSaveShare();
  const remove = useRemoveShare();
  const toast = useToast();
  const linkBox = useRef<HTMLInputElement>(null);
  const share = data?.shares.find((other) => other.file_id === fileId);
  const [role, setRole] = useState<Role>(share?.role ?? "viewer");
  const [expiry, setExpiry] = useState(share?.expires_at ? "keep" : "never");
  const options = share?.expires_at ? [{ id: "keep", label: `Keep ${formatDate(share.expires_at)}`, days: null }, ...EXPIRIES] : EXPIRIES;

  function expiresAt(): string | null {
    if (expiry === "keep") {
      return share?.expires_at ?? null;
    }
    const days = EXPIRIES.find((option) => option.id === expiry)?.days;
    return days ? new Date(Date.now() + days * DAY_MS).toISOString() : null;
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <div className="space-y-1.5">
          <DialogTitle className="truncate text-base font-semibold">Google link for {name}</DialogTitle>
          <DialogDescription className="text-sm text-muted-foreground">
            Anyone who has this link can open the file without signing in, until you remove it. Google serves the file,
            so dextro can't see who opens it.
          </DialogDescription>
        </div>
        {share && (
          <div className="flex items-center gap-2">
            <input
              ref={linkBox}
              readOnly
              aria-label="Google link"
              value={share.url}
              className="min-w-0 flex-1 rounded-lg bg-raised px-2.5 py-1.5 text-sm outline-none"
            />
            <CopyButton text={share.url} label="Copy the link" fallback={() => linkBox.current?.select()} />
          </div>
        )}
        <div role="group" aria-label="What people with the link can do" className="-mx-2.5">
          {ROLES.map((option) => (
            <Choice
              key={option.id}
              chosen={role === option.id}
              onClick={() => setRole(option.id)}
              className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm"
            >
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{option.label}</span>
                <span className="block text-xs text-muted-foreground">{option.text}</span>
              </span>
              {role === option.id && <Check className="size-4 shrink-0" />}
            </Choice>
          ))}
        </div>
        <div className="space-y-1.5">
          <div role="group" aria-label="When the link ends" className="flex flex-wrap gap-1.5">
            {options.map((option) => (
              <Choice
                key={option.id}
                chosen={expiry === option.id}
                onClick={() => setExpiry(option.id)}
                className="rounded-full border px-3 py-1 text-sm"
              >
                {option.label}
              </Choice>
            ))}
          </div>
          <p className="text-xs text-faint">
            dextro ends the link itself, because Google can't. If this server is off at that moment, the link keeps
            working until it's back.
          </p>
        </div>
        <FormError message={save.error?.message ?? remove.error?.message} />
        <div className="flex flex-wrap justify-end gap-2">
          {share && (
            <Button
              variant="destructive"
              className="mr-auto"
              disabled={remove.isPending}
              onClick={() =>
                remove.mutate(fileId, {
                  onSuccess: () => {
                    toast({ message: "Google link removed" });
                    onClose();
                  },
                })
              }
            >
              Remove link
            </Button>
          )}
          <DialogClose asChild>
            <Button variant="soft">
              {share ? "Done" : "Cancel"}
            </Button>
          </DialogClose>
          <Button
            disabled={save.isPending}
            onClick={() =>
              save.mutate(
                { fileId, role, expires_at: expiresAt() },
                // Saving again shouldn't count the days from now, which would quietly extend it.
                { onSuccess: (saved) => setExpiry(saved.expires_at ? "keep" : "never") },
              )
            }
          >
            {share ? "Save changes" : "Make link"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
