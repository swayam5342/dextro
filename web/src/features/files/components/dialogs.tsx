import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import type { components } from "@/api/schema";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogTitle } from "@/components/ui/dialog";

import { contentUrl, folderQuery, useMove, type Item } from "../queries";

type File = components["schemas"]["File"];

interface NameDialogProps {
  title: string;
  label: string;
  submitLabel: string;
  initial: string;
  pending: boolean;
  error: string | undefined;
  onSubmit: (name: string) => void;
  onClose: () => void;
}

export function NameDialog({
  title,
  label,
  submitLabel,
  initial,
  pending,
  error,
  onSubmit,
  onClose,
}: NameDialogProps) {
  const [name, setName] = useState(initial);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    onSubmit(name);
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle className="text-lg font-semibold">{title}</DialogTitle>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field
            id="item-name"
            label={label}
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <FormError message={error} />
          <div className="flex justify-end gap-2">
            <DialogClose asChild>
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={pending}>
              {submitLabel}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface MoveDialogProps {
  item: Item;
  name: string;
  onClose: () => void;
}

/** A small folder browser: open folders until you're where the item should go. */
export function MoveDialog({ item, name, onClose }: MoveDialogProps) {
  const [folderId, setFolderId] = useState<string | undefined>(undefined);
  const { data, isPending, isError } = useQuery(folderQuery(folderId));
  const move = useMove();
  const parent = data?.path.at(-2)?.id;
  const here = data?.path.at(-1);
  // A folder can't go inside itself, so it isn't offered as a place to open.
  const folders = data?.folders.filter((f) => !(item.kind === "folder" && f.id === item.id)) ?? [];

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle className="truncate text-lg font-semibold">Move {name}</DialogTitle>
        <p className="text-sm text-muted-foreground">
          Moving to <span className="font-medium text-foreground">{here?.name ?? "Files"}</span>
        </p>
        {isError && <FormError message="This folder couldn't be loaded." />}
        <ul className="max-h-60 divide-y overflow-y-auto rounded-md border">
          {here && (
            <li>
              <button
                type="button"
                className="w-full px-3 py-2 text-left text-sm hover:bg-accent"
                onClick={() => setFolderId(parent)}
              >
                Back
              </button>
            </li>
          )}
          {folders.map((folder) => (
            <li key={folder.id}>
              <button
                type="button"
                className="w-full truncate px-3 py-2 text-left text-sm hover:bg-accent"
                onClick={() => setFolderId(folder.id)}
              >
                {folder.name}
              </button>
            </li>
          ))}
          {!isPending && !here && folders.length === 0 && (
            <li className="px-3 py-2 text-sm text-muted-foreground">There are no folders here.</li>
          )}
        </ul>
        <FormError message={move.error?.message} />
        <div className="flex justify-end gap-2">
          <DialogClose asChild>
            <Button type="button" variant="outline">
              Cancel
            </Button>
          </DialogClose>
          <Button
            disabled={move.isPending || isPending}
            onClick={() =>
              move.mutate({ item, target: folderId ?? null }, { onSuccess: onClose })
            }
          >
            Move here
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function PreviewDialog({ file, onClose }: { file: File; onClose: () => void }) {
  const url = contentUrl(file.id, true);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined} className="max-w-4xl">
        <DialogTitle className="truncate pr-4 text-lg font-semibold">{file.name}</DialogTitle>
        {file.preview === "image" && (
          <img src={url} alt={file.name} className="mx-auto max-h-[70svh] object-contain" />
        )}
        {file.preview === "video" && (
          // oxlint-disable-next-line jsx-a11y/media-has-caption -- users' own files have no captions.
          <video src={url} controls className="mx-auto max-h-[70svh] w-full" />
        )}
        <div className="flex justify-end gap-2">
          <Button asChild variant="outline">
            <a href={contentUrl(file.id, false)} download>
              Download
            </a>
          </Button>
          <DialogClose asChild>
            <Button>Close</Button>
          </DialogClose>
        </div>
      </DialogContent>
    </Dialog>
  );
}
