import { useQuery } from "@tanstack/react-query";
import { Pause, Play, Upload as UploadIcon, X } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { connectionsQuery } from "@/features/storage/queries";

import { useUploads, type Upload, type UploadStatus } from "../uploads";

function destination(upload: Upload, emails: Map<string, string>): string {
  const account = upload.connectionId ? emails.get(upload.connectionId) : undefined;
  if (upload.status === "done") {
    return `In ${account ?? "your pool"}`;
  }
  return account ? `To ${account}` : "To the account with the most free space";
}

function RowButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      className="grid size-6 place-items-center rounded-full hover:bg-raised hover:text-foreground"
      aria-label={label}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function Row({ upload, emails }: { upload: Upload; emails: Map<string, string> }) {
  const { cancel, pause, resume, reattach } = useUploads();
  const picker = useRef<HTMLInputElement>(null);
  const percent = upload.total > 0 ? Math.round((upload.loaded / upload.total) * 100) : 100;
  const running = upload.status === "waiting" || upload.status === "uploading" || upload.status === "retrying";
  const stopped = upload.status === "paused" || upload.status === "needs-file";
  const labels: Record<UploadStatus, string> = {
    waiting: "Waiting",
    uploading: `${percent}%`,
    retrying: "Reconnecting…",
    paused: `Paused at ${percent}%`,
    "needs-file": `Stopped at ${percent}%`,
    done: "Done",
    failed: "Failed",
    cancelled: "Cancelled",
  };

  function handlePick(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file) {
      reattach(upload.id, file);
    }
  }

  return (
    <li className="space-y-1.5">
      <div className="flex items-center justify-between gap-3 text-[13px]">
        <span className="truncate">{upload.name}</span>
        <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
          {labels[upload.status]}
          {running && (
            <RowButton label={`Pause ${upload.name}`} onClick={() => pause(upload.id)}>
              <Pause className="size-3.5" />
            </RowButton>
          )}
          {upload.status === "paused" && (
            <RowButton label={`Resume ${upload.name}`} onClick={() => resume(upload.id)}>
              <Play className="size-3.5" />
            </RowButton>
          )}
          {(running || stopped) && (
            <RowButton label={`Cancel ${upload.name}`} onClick={() => cancel(upload.id)}>
              <X className="size-3.5" />
            </RowButton>
          )}
        </span>
      </div>
      {(running || stopped) && (
        <progress className="level block" value={percent} max={100} aria-label={`Progress of ${upload.name}`} />
      )}
      {upload.status === "needs-file" && (
        <>
          <input ref={picker} type="file" hidden aria-label={`Choose ${upload.name} again`} onChange={handlePick} />
          <Button variant="outline" size="sm" className="bg-raised hover:bg-[#3a3a3a]" onClick={() => picker.current?.click()}>
            Pick the file again to continue
          </Button>
        </>
      )}
      {upload.error ? (
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
  const count = (...statuses: UploadStatus[]) => uploads.filter((u) => statuses.includes(u.status)).length;
  const left = count("waiting", "uploading", "retrying");
  const stopped = count("paused", "needs-file");
  const finished = count("done", "failed", "cancelled");
  const failed = count("failed");
  let title = "Uploads finished";
  if (left > 0) {
    title = `Uploading ${left} of ${uploads.length}`;
  } else if (stopped > 0) {
    title = stopped === 1 ? "1 upload stopped" : `${stopped} uploads stopped`;
  } else if (failed > 0) {
    title = `${failed} failed`;
  }
  return (
    <section
      aria-label="Uploads"
      className="fixed right-4 bottom-[74px] z-30 w-[min(360px,calc(100vw-2rem))] overflow-hidden rounded-2xl bg-card shadow-[0_12px_32px_rgba(0,0,0,0.7)] md:right-5 md:bottom-5"
    >
      <header className="flex items-center gap-2 py-2 pr-2 pl-4 font-semibold">
        <span className="flex-1">{title}</span>
        <Button variant="ghost" size="icon" disabled={finished === 0} aria-label="Clear finished uploads" onClick={clearFinished}>
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
