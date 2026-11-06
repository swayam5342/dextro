import { ChevronLeft, ChevronRight, Download, ExternalLink, Trash2, X } from "lucide-react";
import { useState, type KeyboardEvent } from "react";

import type { components } from "@/api/schema";
import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogBare, DialogClose, DialogPortal, DialogTitle } from "@/components/ui/dialog";
import { formatBytes, formatDate } from "@/lib/format";

import { contentUrl } from "../queries";
import { fileIcon } from "./file-icon";

type File = components["schemas"]["File"];

/** What can be shown of a file. It's keyed by file, so a failed load doesn't stick. */
function Stage({ file }: { file: File }) {
  const [failed, setFailed] = useState(false);
  const url = contentUrl(file.id, true);

  if (!failed && file.preview === "image") {
    return (
      <img src={url} alt={file.name} className="max-h-full max-w-full object-contain" onError={() => setFailed(true)} />
    );
  }
  if (!failed && file.preview === "video") {
    return (
      // oxlint-disable-next-line jsx-a11y/media-has-caption -- users' own files have no captions.
      <video src={url} controls className="max-h-full max-w-full" onError={() => setFailed(true)} />
    );
  }

  const Icon = fileIcon(file.content_type);
  return (
    <div className="grid justify-items-center gap-3 text-center text-muted-foreground">
      <Icon className="size-16" />
      {failed ? (
        <p>This file couldn't be loaded from its account.</p>
      ) : file.preview === "pdf" ? (
        <Button asChild variant="outline">
          <a href={url} target="_blank" rel="noopener noreferrer">
            <ExternalLink />
            Open in a new tab
          </a>
        </Button>
      ) : (
        <p>This type of file can't be shown here. Download it to open it.</p>
      )}
    </div>
  );
}

interface ViewerProps {
  file: File;
  previous: File | undefined;
  next: File | undefined;
  accountName: (connectionId: string) => string;
  trashError: string | undefined;
  onShow: (file: File) => void;
  onTrash: (file: File) => void;
  onClose: () => void;
}

/** Shows one file at full size, with the arrow keys moving through the folder. */
export function Viewer({ file, previous, next, accountName, trashError, onShow, onTrash, onClose }: ViewerProps) {
  function handleKeyDown(event: KeyboardEvent) {
    // A focused video uses the arrow keys to seek.
    if (event.target instanceof HTMLVideoElement) {
      return;
    }
    if (event.key === "ArrowLeft" && previous) {
      onShow(previous);
    }
    if (event.key === "ArrowRight" && next) {
      onShow(next);
    }
  }

  const details: [string, string][] = [
    ["Size", formatBytes(file.size)],
    ["Type", file.content_type],
    ["Stored in", accountName(file.connection_id)],
    ["Added", formatDate(file.created_at)],
  ];

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogPortal>
        <DialogBare
          aria-describedby={undefined}
          onKeyDown={handleKeyDown}
          onPointerDownOutside={(event) => {
            // Undo sits outside the dialog, and pressing it shouldn't close the viewer.
            if (event.target instanceof Element && event.target.closest("[data-toast]")) {
              event.preventDefault();
            }
          }}
          className="fixed inset-0 z-50 grid grid-cols-[minmax(0,1fr)] grid-rows-[56px_minmax(0,1fr)_auto] bg-black outline-none md:grid-cols-[minmax(0,1fr)_300px] md:grid-rows-[56px_minmax(0,1fr)]"
        >
          <div className="col-span-full flex min-w-0 items-center gap-2 border-b px-4">
            <DialogTitle className="min-w-0 flex-1 truncate text-sm font-medium">{file.name}</DialogTitle>
            <Button asChild variant="ghost" size="icon" aria-label={`Download ${file.name}`}>
              <a href={contentUrl(file.id, false)} download>
                <Download />
              </a>
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Move ${file.name} to the trash`} onClick={() => onTrash(file)}>
              <Trash2 />
            </Button>
            <DialogClose asChild>
              <Button variant="ghost" size="icon" aria-label="Close">
                <X />
              </Button>
            </DialogClose>
          </div>
          <div className="relative grid min-h-0 place-items-center p-4">
            <Stage key={file.id} file={file} />
            {previous && (
              <Button variant="outline" size="icon" className="absolute top-1/2 left-4 -translate-y-1/2" aria-label="Previous file" onClick={() => onShow(previous)}>
                <ChevronLeft />
              </Button>
            )}
            {next && (
              <Button variant="outline" size="icon" className="absolute top-1/2 right-4 -translate-y-1/2" aria-label="Next file" onClick={() => onShow(next)}>
                <ChevronRight />
              </Button>
            )}
          </div>
          <aside className="max-h-[34svh] overflow-y-auto border-t p-4 md:max-h-none md:border-t-0 md:border-l md:p-5">
            <h2 className="mb-3.5 hidden text-base font-semibold md:block">Details</h2>
            <dl className="grid grid-cols-2 gap-3 md:grid-cols-1">
              {details.map(([label, value]) => (
                <div key={label} className="min-w-0">
                  <dt className="text-xs text-faint">{label}</dt>
                  <dd className="text-sm break-words">{value}</dd>
                </div>
              ))}
            </dl>
            <div className="mt-3">
              <FormError message={trashError} />
            </div>
          </aside>
        </DialogBare>
      </DialogPortal>
    </Dialog>
  );
}
