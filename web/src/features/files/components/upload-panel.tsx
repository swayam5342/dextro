import { useQuery } from "@tanstack/react-query";
import { Upload as UploadIcon, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { connectionsQuery } from "@/features/storage/queries";

import { useUploads, type Upload } from "../uploads";

function destination(upload: Upload, emails: Map<string, string>): string {
  const account = upload.connectionId ? emails.get(upload.connectionId) : undefined;
  if (upload.status === "done") {
    return `In ${account ?? "your pool"}`;
  }
  return account ? `To ${account}` : "To the account with the most free space";
}

function Row({ upload, emails }: { upload: Upload; emails: Map<string, string> }) {
  const { cancel } = useUploads();
  const percent = upload.total > 0 ? Math.round((upload.loaded / upload.total) * 100) : 100;
  const inProgress = upload.status === "waiting" || upload.status === "uploading";
  const labels = {
    waiting: "Waiting",
    uploading: `${percent}%`,
    done: "Done",
    failed: "Failed",
    cancelled: "Cancelled",
  };
  return (
    <li className="space-y-1.5">
      <div className="flex items-center justify-between gap-3 text-[13px]">
        <span className="truncate">{upload.name}</span>
        <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
          {labels[upload.status]}
          {inProgress && (
            <button
              type="button"
              className="grid size-6 place-items-center rounded-full hover:bg-raised hover:text-foreground"
              aria-label={`Cancel ${upload.name}`}
              onClick={() => cancel(upload.id)}
            >
              <X className="size-3.5" />
            </button>
          )}
        </span>
      </div>
      {inProgress && (
        <progress className="level block" value={percent} max={100} aria-label={`Uploading ${upload.name}`} />
      )}
      {upload.status === "failed" ? (
        <p role="alert" className="text-xs text-destructive">
          {upload.error}
        </p>
      ) : (
        upload.status !== "cancelled" && <p className="text-xs text-faint">{destination(upload, emails)}</p>
      )}
    </li>
  );
}

/** Progress for every upload, docked in the corner on every page. */
export function UploadPanel() {
  const { uploads, clearFinished } = useUploads();
  const { data: connections } = useQuery(connectionsQuery);
  if (uploads.length === 0) {
    return null;
  }
  const emails = new Map(connections?.map((c) => [c.id, c.summary]));
  const left = uploads.filter((u) => u.status === "waiting" || u.status === "uploading").length;
  const failed = uploads.filter((u) => u.status === "failed").length;
  const finishedText = failed > 0 ? `${failed} failed` : "Uploads finished";
  return (
    <section
      aria-label="Uploads"
      className="fixed right-4 bottom-[74px] z-30 w-[min(360px,calc(100vw-2rem))] overflow-hidden rounded-2xl bg-card shadow-[0_12px_32px_rgba(0,0,0,0.7)] md:right-5 md:bottom-5"
    >
      <header className="flex items-center gap-2 py-2 pr-2 pl-4 font-semibold">
        <span className="flex-1">{left > 0 ? `Uploading ${left} of ${uploads.length}` : finishedText}</span>
        <Button variant="ghost" size="icon" disabled={left === uploads.length} aria-label="Clear finished uploads" onClick={clearFinished}>
          <X />
        </Button>
      </header>
      <ul className="grid max-h-60 gap-3 overflow-y-auto px-4 pb-4">
        {uploads.map((upload) => (
          <Row key={upload.id} upload={upload} emails={emails} />
        ))}
      </ul>
    </section>
  );
}

/** Dropping files anywhere in the app uploads them to the folder on screen. */
export function DropZone({ folderId, hint }: { folderId: string | undefined; hint: string }) {
  const { add } = useUploads();
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);

  useEffect(() => {
    const hasFiles = (event: DragEvent) => event.dataTransfer?.types.includes("Files") ?? false;
    const enter = (event: DragEvent) => {
      if (hasFiles(event)) {
        event.preventDefault();
        depth.current += 1;
        setDragging(true);
      }
    };
    const over = (event: DragEvent) => {
      if (hasFiles(event)) {
        event.preventDefault();
      }
    };
    const leave = () => {
      depth.current = Math.max(depth.current - 1, 0);
      if (depth.current === 0) {
        setDragging(false);
      }
    };
    const drop = (event: DragEvent) => {
      event.preventDefault();
      depth.current = 0;
      setDragging(false);
      const files = Array.from(event.dataTransfer?.files ?? []);
      if (files.length > 0) {
        add(files, folderId);
      }
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
    };
  }, [add, folderId]);

  if (!dragging) {
    return null;
  }
  return (
    <div className="pointer-events-none fixed inset-0 z-40 grid place-items-center bg-black/80">
      <div className="text-center">
        <UploadIcon className="mx-auto size-11 text-water" />
        <p className="mt-3 text-xl font-semibold">Drop to upload</p>
        <p className="text-sm text-muted-foreground">{hint}</p>
      </div>
    </div>
  );
}
