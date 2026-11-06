import { useQuery } from "@tanstack/react-query";
import { Check, ChevronLeft, Folder } from "lucide-react";
import { useState, type FormEvent } from "react";

import { Choice } from "@/components/choice";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { freeSpaceText, usableConnections } from "@/features/storage/pool";
import { connectionsQuery, poolQuery, useTransferFiles } from "@/features/storage/queries";

import { folderQuery, useMove, type Item } from "../queries";

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
        <DialogTitle className="truncate text-base font-semibold">{title}</DialogTitle>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field
            id="item-name"
            label={label}
            className="bg-raised"
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <FormError message={error} />
          <div className="flex justify-end gap-2">
            <DialogClose asChild>
              <Button type="button" variant="soft">
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

interface AccountDialogProps {
  fileIds: string[];
  name: string;
  /** The accounts the files are in now. */
  from: string[];
  /** How many actually started moving, and to which account. */
  onDone: (queued: number, account: string) => void;
  onClose: () => void;
}

/** Picks the account files should live in, which is separate from their folder. */
export function AccountDialog({ fileIds, name, from, onDone, onClose }: AccountDialogProps) {
  const { data: connections } = useQuery(connectionsQuery);
  const { data: pool } = useQuery(poolQuery);
  const transfer = useTransferFiles();
  const [target, setTarget] = useState<string>();
  // Where they all already are isn't a choice. Files from several accounts can go to any.
  const here = from.length === 1 ? from[0] : undefined;
  const accounts = usableConnections(connections).filter((connection) => connection.id !== here);
  const chosen = accounts.find((account) => account.id === target);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle className="truncate text-base font-semibold">Move {name} to another account</DialogTitle>
        <p className="text-sm text-muted-foreground">
          It stays in the same folder here. The move runs in the background, and the file is copied and checked before
          it's removed from where it is now.
        </p>
        <FormError message={transfer.error?.message} />
        <ul className="-mx-2.5 max-h-60 overflow-y-auto">
          {accounts.map((account) => (
            <li key={account.id}>
              <Choice
                chosen={account.id === target}
                className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm"
                onClick={() => setTarget(account.id)}
              >
                <span className="min-w-0 flex-1 truncate">{account.summary}</span>
                <span className="shrink-0 text-xs text-muted-foreground">{freeSpaceText(pool?.accounts.find((a) => a.connection.id === account.id))}</span>
                {account.id === target && <Check className="size-4" />}
              </Choice>
            </li>
          ))}
        </ul>
        <div className="flex justify-end gap-2">
          <DialogClose asChild>
            <Button variant="soft">
              Cancel
            </Button>
          </DialogClose>
          <Button
            disabled={!chosen || transfer.isPending}
            onClick={() => {
              if (chosen) {
                transfer.mutate(
                  { file_ids: fileIds, target_connection_id: chosen.id },
                  { onSuccess: ({ queued }) => onDone(queued, chosen.summary) },
                );
              }
            }}
          >
            Move
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface MoveDialogProps {
  items: Item[];
  name: string;
  onDone: () => void;
  onClose: () => void;
}

/** A small folder browser: open folders until you're where the items should go. */
export function MoveDialog({ items, name, onDone, onClose }: MoveDialogProps) {
  const [folderId, setFolderId] = useState<string | undefined>(undefined);
  const { data, isPending, isError } = useQuery(folderQuery(folderId));
  const move = useMove();
  const [moving, setMoving] = useState(false);
  const parent = data?.path.at(-2)?.id;
  const here = data?.path.at(-1);
  // A folder can't go inside itself, so it isn't offered as a place to open.
  const moved = new Set(items.filter((item) => item.kind === "folder").map((item) => item.id));
  const folders = data?.folders.filter((folder) => !moved.has(folder.id)) ?? [];
  const rowStyle = "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm hover:bg-raised [&_svg]:size-[18px] [&_svg]:shrink-0";

  /** One at a time, so a failure stops the move and its message stays on screen. */
  async function handleMove() {
    setMoving(true);
    try {
      for (const item of items) {
        await move.mutateAsync({ item, target: folderId ?? null });
      }
      onDone();
    } catch {
      setMoving(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle className="truncate text-base font-semibold">Move {name}</DialogTitle>
        <p className="text-sm text-muted-foreground">
          Moving to <span className="font-medium text-foreground">{here?.name ?? "Files"}</span>
        </p>
        {isError && <FormError message="This folder couldn't be loaded." />}
        <ul className="-mx-2.5 max-h-60 overflow-y-auto">
          {here && (
            <li>
              <button type="button" className={rowStyle} onClick={() => setFolderId(parent)}>
                <ChevronLeft />
                Back
              </button>
            </li>
          )}
          {folders.map((folder) => (
            <li key={folder.id}>
              <button type="button" className={rowStyle} onClick={() => setFolderId(folder.id)}>
                <Folder className="text-muted-foreground" />
                <span className="truncate">{folder.name}</span>
              </button>
            </li>
          ))}
          {!isPending && folders.length === 0 && (
            <li className="px-2.5 py-2 text-sm text-muted-foreground">There are no folders here.</li>
          )}
        </ul>
        <FormError message={move.error?.message} />
        <div className="flex justify-end gap-2">
          <DialogClose asChild>
            <Button type="button" variant="soft">
              Cancel
            </Button>
          </DialogClose>
          <Button disabled={moving || isPending} onClick={() => void handleMove()}>
            Move here
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
