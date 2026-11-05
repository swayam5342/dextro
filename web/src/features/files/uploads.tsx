import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

import { ApiError, toApiError } from "@/api/errors";
import { poolQuery } from "@/features/storage/queries";

import { useRefreshFiles } from "./queries";

export interface Upload {
  id: number;
  name: string;
  loaded: number;
  total: number;
  status: "waiting" | "uploading" | "done" | "failed" | "cancelled";
  error?: string;
  /** The account picked for it, then the account it went to. */
  connectionId?: string;
}

interface UploadsValue {
  uploads: Upload[];
  /** The account uploads go to. Empty means the one with the most free space. */
  target: string;
  setTarget: (connectionId: string) => void;
  add: (files: File[], folderId: string | undefined) => void;
  cancel: (id: number) => void;
  clearFinished: () => void;
}

const UploadsContext = createContext<UploadsValue | null>(null);

export function useUploads(): UploadsValue {
  const value = useContext(UploadsContext);
  if (!value) {
    throw new Error("useUploads needs an UploadsProvider");
  }
  return value;
}

interface Destination {
  connectionId: string | undefined;
  folderId: string | undefined;
}

/**
 * Sends a file as the request body. This uses XMLHttpRequest because fetch can't
 * report upload progress, and the body is the file itself so it streams.
 */
function send(file: File, to: Destination, onProgress: (loaded: number) => void, request: XMLHttpRequest) {
  return new Promise<string>((resolve, reject) => {
    const query = new URLSearchParams({ name: file.name });
    if (to.connectionId) {
      query.set("connection_id", to.connectionId);
    }
    if (to.folderId) {
      query.set("folder_id", to.folderId);
    }
    request.open("POST", `/api/v1/files?${query.toString()}`);
    request.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    request.upload.onprogress = (event) => onProgress(event.loaded);
    request.onload = () => {
      let body: unknown;
      try {
        body = JSON.parse(request.responseText);
      } catch {
        body = undefined;
      }
      if (request.status >= 200 && request.status < 300) {
        resolve((body as { connection_id: string }).connection_id);
        return;
      }
      reject(toApiError(body));
    };
    request.onerror = () => reject(new ApiError("network", "The connection was lost during the upload."));
    request.onabort = () => reject(new ApiError("cancelled", "Cancelled."));
    request.send(file);
  });
}

/**
 * Holds uploads above the pages, so they keep running while someone moves around
 * the app. They run one at a time, so files don't fight over the connection.
 */
export function UploadsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const refresh = useRefreshFiles();
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [target, setTarget] = useState("");
  const nextId = useRef(0);
  const queue = useRef<{ id: number; file: File; to: Destination }[]>([]);
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
      const { id, file, to } = next;
      const request = new XMLHttpRequest();
      active.current = { id, request };
      update(id, { status: "uploading" });
      try {
        const connectionId = await send(file, to, (loaded) => update(id, { loaded }), request);
        update(id, { status: "done", loaded: file.size, connectionId });
        await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: poolQuery.queryKey })]);
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
  }, [queryClient, refresh, update]);

  const add = useCallback(
    (files: File[], folderId: string | undefined) => {
      const to = { connectionId: target || undefined, folderId };
      const added = files.map((file) => {
        nextId.current += 1;
        queue.current.push({ id: nextId.current, file, to });
        return {
          id: nextId.current,
          name: file.name,
          loaded: 0,
          total: file.size,
          status: "waiting" as const,
          connectionId: to.connectionId,
        };
      });
      setUploads((current) => [...current, ...added]);
      void run();
    },
    [run, target],
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

  const value = useMemo(
    () => ({ uploads, target, setTarget, add, cancel, clearFinished }),
    [uploads, target, add, cancel, clearFinished],
  );
  return <UploadsContext.Provider value={value}>{children}</UploadsContext.Provider>;
}
