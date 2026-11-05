import { useCallback, useRef, useState } from "react";

import { ApiError, toApiError } from "@/api/errors";

import { useRefreshFiles } from "./queries";

export interface Upload {
  id: number;
  name: string;
  loaded: number;
  total: number;
  status: "waiting" | "uploading" | "done" | "failed" | "cancelled";
  error?: string;
}

interface Target {
  /** The account to use. Left undefined, the server picks the one with the most free space. */
  connectionId: string | undefined;
  folderId: string | undefined;
}

/**
 * Sends a file as the request body. This uses XMLHttpRequest because fetch can't
 * report upload progress, and the body is the file itself so it streams.
 */
function send(file: File, target: Target, onProgress: (loaded: number) => void, request: XMLHttpRequest) {
  return new Promise<void>((resolve, reject) => {
    const query = new URLSearchParams({ name: file.name });
    if (target.connectionId) {
      query.set("connection_id", target.connectionId);
    }
    if (target.folderId) {
      query.set("folder_id", target.folderId);
    }
    request.open("POST", `/api/v1/files?${query.toString()}`);
    request.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    request.upload.onprogress = (event) => onProgress(event.loaded);
    request.onload = () => {
      if (request.status >= 200 && request.status < 300) {
        resolve();
        return;
      }
      let body: unknown;
      try {
        body = JSON.parse(request.responseText);
      } catch {
        body = undefined;
      }
      reject(toApiError(body));
    };
    request.onerror = () => reject(new ApiError("network", "The connection was lost during the upload."));
    request.onabort = () => reject(new ApiError("cancelled", "Cancelled."));
    request.send(file);
  });
}

/** Runs uploads one at a time, so several files don't fight over the connection. */
export function useUploads() {
  const refresh = useRefreshFiles();
  const [uploads, setUploads] = useState<Upload[]>([]);
  const nextId = useRef(0);
  const queue = useRef<{ id: number; file: File; target: Target }[]>([]);
  const running = useRef(false);
  const active = useRef<{ id: number; request: XMLHttpRequest } | null>(null);

  const update = useCallback((id: number, change: Partial<Upload>) => {
    setUploads((current) => current.map((u) => (u.id === id ? { ...u, ...change } : u)));
  }, []);

  const run = useCallback(async () => {
    if (running.current) {
      return;
    }
    running.current = true;
    for (let next = queue.current.shift(); next; next = queue.current.shift()) {
      const { id, file, target } = next;
      const request = new XMLHttpRequest();
      active.current = { id, request };
      update(id, { status: "uploading" });
      try {
        await send(file, target, (loaded) => update(id, { loaded }), request);
        update(id, { status: "done", loaded: file.size });
        await refresh();
      } catch (error) {
        const failure = error instanceof ApiError ? error : toApiError(error);
        update(id, {
          status: failure.code === "cancelled" ? "cancelled" : "failed",
          error: failure.message,
        });
      }
    }
    active.current = null;
    running.current = false;
  }, [refresh, update]);

  const add = useCallback(
    (files: File[], target: Target) => {
      const added = files.map((file) => {
        nextId.current += 1;
        queue.current.push({ id: nextId.current, file, target });
        return {
          id: nextId.current,
          name: file.name,
          loaded: 0,
          total: file.size,
          status: "waiting" as const,
        };
      });
      setUploads((current) => [...current, ...added]);
      void run();
    },
    [run],
  );

  const cancel = useCallback(
    (id: number) => {
      if (active.current?.id === id) {
        active.current.request.abort();
        return;
      }
      queue.current = queue.current.filter((item) => item.id !== id);
      update(id, { status: "cancelled" });
    },
    [update],
  );

  const clearFinished = useCallback(() => {
    setUploads((current) => current.filter((u) => u.status === "waiting" || u.status === "uploading"));
  }, []);

  return { uploads, add, cancel, clearFinished };
}
