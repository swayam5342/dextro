import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { api } from "@/api/client";
import { ApiError, failureMessage, toApiError } from "@/api/errors";
import type { components } from "@/api/schema";
import { poolQuery, routingQuery } from "@/features/storage/queries";

import { foldersToMake, type FolderTree } from "./folder-tree";
import { createFolder, folderQuery, useRefreshFiles } from "./queries";

type Session = components["schemas"]["UploadSession"];
type Folder = components["schemas"]["Folder"];

/** A multiple of 256 KiB, which Drive insists on for every piece but the last. */
const CHUNK_BYTES = 8 * 1024 * 1024;
const MAX_RUNNING = 3;
const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 16000, 30000];
/** Worth another try. A full account (507) isn't: waiting won't make room. */
const RETRY_STATUSES = new Set([0, 429, 500, 502, 503, 504]);

export type UploadStatus =
  | "preparing"
  | "waiting"
  | "uploading"
  | "retrying"
  | "paused"
  | "needs-file"
  | "done"
  | "failed"
  | "cancelled";

export interface Upload {
  id: number;
  name: string;
  loaded: number;
  total: number;
  status: UploadStatus;
  error?: string;
  /** The account picked for it, then the account it went to. */
  connectionId?: string;
}

/** Asked when a folder going up has files that are already there. */
export interface DuplicatesQuestion {
  count: number;
  answer: (skip: boolean) => void;
}

interface UploadsValue {
  uploads: Upload[];
  duplicates: DuplicatesQuestion | null;
  /** The account uploads go to. Empty leaves it to the user's routing. */
  target: string;
  setTarget: (connectionId: string) => void;
  add: (files: File[], folderId: string | undefined) => void;
  /** Recreates a folder from the computer, then uploads what's in it. */
  addTree: (name: string, tree: FolderTree | Promise<FolderTree>, folderId: string | undefined) => Promise<void>;
  pause: (id: number) => void;
  resume: (id: number) => void;
  /** Gives an upload from before a reload its file back. */
  reattach: (id: number, file: File) => void;
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

/** What the engine knows about an upload, beyond what the panel shows. */
interface Job {
  id: number;
  file?: File;
  connectionId: string | undefined;
  folderId: string | undefined;
  sessionId?: string;
  name: string;
  size: number;
  lastModified: number;
  request?: XMLHttpRequest;
  /** Cuts a retry's wait short. */
  wake?: () => void;
  stopping?: "pause" | "cancel";
  /** Paused by a lost connection, so it can pick up again by itself. */
  pausedByNetwork?: boolean;
}

/** Means the upload was paused or cancelled, which is nobody's error. */
class Stopped extends Error {}

/** Worth trying again: the connection dropped, or the server had a bad moment. */
class Retryable extends Error {}

function failureFrom(status: number, body: unknown): Error {
  if (RETRY_STATUSES.has(status)) {
    return new Retryable("The connection was lost during the upload.");
  }
  return toApiError(body);
}

function parse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/**
 * Sends a request body and reports progress, which fetch can't do. The body is a
 * File or a slice of one, so the browser streams it.
 */
function transfer<T>(job: Job, open: (request: XMLHttpRequest) => void, body: Blob, onProgress: (sent: number) => void) {
  return new Promise<T>((resolve, reject) => {
    const request = new XMLHttpRequest();
    job.request = request;
    open(request);
    request.upload.onprogress = (event) => onProgress(event.loaded);
    request.onload = () => {
      const parsed = parse(request.responseText);
      if (request.status >= 200 && request.status < 300) {
        resolve(parsed as T);
        return;
      }
      if (request.status === 409) {
        reject(new Retryable("This piece didn't start where the upload was."));
        return;
      }
      reject(failureFrom(request.status, parsed));
    };
    request.onerror = () => reject(new Retryable("The connection was lost during the upload."));
    request.onabort = () => reject(new Stopped());
    request.send(body);
  });
}

async function beginSession(job: Job, file: File): Promise<Session> {
  try {
    const { data, error, response } = await api.POST("/api/v1/uploads", {
      body: {
        name: job.name,
        size: job.size,
        content_type: file.type || undefined,
        folder_id: job.folderId ?? null,
        connection_id: job.connectionId ?? null,
        last_modified: job.lastModified,
      },
    });
    if (data) {
      return data;
    }
    throw failureFrom(response.status, error);
  } catch (error) {
    throw error instanceof ApiError || error instanceof Retryable ? error : new Retryable("The connection was lost.");
  }
}

async function readSession(id: string): Promise<Session> {
  try {
    const { data, error, response } = await api.GET("/api/v1/uploads/{id}", { params: { path: { id } } });
    if (data) {
      return data;
    }
    throw failureFrom(response.status, error);
  } catch (error) {
    throw error instanceof ApiError || error instanceof Retryable ? error : new Retryable("The connection was lost.");
  }
}

/**
 * Makes a tree's folders and says which is which, by path. A folder that's already
 * there is used, so uploading the rest of a folder doesn't make a second copy of it.
 */
async function makeFolders(queryClient: QueryClient, tree: FolderTree, root: string | undefined) {
  const ids = new Map<string, string | undefined>([["", root]]);
  const made = new Set<string>();
  const listings = new Map<string | undefined, Folder[]>();
  for (const path of foldersToMake(tree)) {
    const name = path.at(-1) ?? "";
    const parent = ids.get(path.slice(0, -1).join("/"));
    const siblings = listings.get(parent) ?? [...(await queryClient.fetchQuery(folderQuery(parent))).folders];
    listings.set(parent, siblings);
    let folder = siblings.find((other) => other.name === name);
    if (!folder) {
      folder = await createFolder({ name, parent_id: parent ?? null });
      made.add(folder.id);
      siblings.push(folder);
      // Nothing can be in it yet, so there's no need to ask.
      listings.set(folder.id, []);
    }
    ids.set(path.join("/"), folder.id);
  }
  return { ids, made };
}

/** Which of a tree's files are already in their folder, by name and size. */
async function filesAlreadyThere(queryClient: QueryClient, tree: FolderTree, ids: Map<string, string | undefined>, made: Set<string>) {
  const stored = new Map<string | undefined, Set<string>>();
  const already = new Set<File>();
  for (const { file, dirs } of tree.files) {
    const folderId = ids.get(dirs.join("/"));
    // A folder made just now can't have anything in it.
    if (folderId !== undefined && made.has(folderId)) {
      continue;
    }
    let names = stored.get(folderId);
    if (!names) {
      const listing = await queryClient.fetchQuery(folderQuery(folderId));
      names = new Set(listing.files.map(({ name, size }) => `${name}:${size}`));
      stored.set(folderId, names);
    }
    if (names.has(`${file.name}:${file.size}`)) {
      already.add(file);
    }
  }
  return already;
}

/**
 * Holds uploads above the pages, so they keep running while someone moves around
 * the app. Each file goes up in pieces and can pick up where it stopped, because
 * the server tracks how much the account really has.
 */
export function UploadsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const refresh = useRefreshFiles();
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [target, setTarget] = useState("");
  const [duplicates, setDuplicates] = useState<DuplicatesQuestion | null>(null);
  const nextId = useRef(0);
  const jobs = useRef(new Map<number, Job>());
  const queue = useRef<number[]>([]);
  const running = useRef(0);

  const update = useCallback((id: number, change: Partial<Upload>) => {
    setUploads((current) => current.map((u) => (u.id === id ? { ...u, ...change } : u)));
  }, []);

  /** Waits before a retry. Pausing or cancelling ends the wait straight away. */
  const wait = useCallback((job: Job, ms: number) => {
    return new Promise<void>((resolve) => {
      const timer = setTimeout(resolve, ms);
      job.wake = () => {
        clearTimeout(timer);
        resolve();
      };
    });
  }, []);

  const sendEmpty = useCallback(async (job: Job, file: File) => {
    const query = new URLSearchParams({ name: job.name });
    if (job.connectionId) {
      query.set("connection_id", job.connectionId);
    }
    if (job.folderId) {
      query.set("folder_id", job.folderId);
    }
    const stored = await transfer<{ connection_id: string }>(
      job,
      (request) => {
        request.open("POST", `/api/v1/files?${query.toString()}`);
        request.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      },
      file,
      () => {},
    );
    update(job.id, { connectionId: stored.connection_id });
  }, [update]);

  const sendChunks = useCallback(async (job: Job, file: File) => {
    let offset = 0;
    let attempt = 0;
    // A session from before a pause or reload may have moved on without us.
    let inSync = job.sessionId === undefined;

    for (;;) {
      try {
        if (job.stopping) {
          throw new Stopped();
        }
        if (!job.sessionId) {
          const session = await beginSession(job, file);
          job.sessionId = session.id;
          update(job.id, { connectionId: session.connection_id });
          // Cancelled while this was starting, so nobody else knows to give the space back.
          if (job.stopping === "cancel") {
            void api.DELETE("/api/v1/uploads/{id}", { params: { path: { id: session.id } } });
          }
          continue;
        }
        if (!inSync) {
          const session = await readSession(job.sessionId);
          if (session.file) {
            return;
          }
          offset = session.received;
          inSync = true;
          update(job.id, { loaded: offset, status: "uploading", error: undefined });
        }

        const start = offset;
        const end = Math.min(start + CHUNK_BYTES, job.size);
        const id = job.sessionId;
        const session = await transfer<Session>(
          job,
          (request) => request.open("PUT", `/api/v1/uploads/${id}/chunk?offset=${start}`),
          file.slice(start, end),
          (sent) => update(job.id, { loaded: start + sent }),
        );
        if (session.file) {
          return;
        }
        // What the account kept, which can be less than what was just sent.
        offset = session.received;
        attempt = 0;
        update(job.id, { loaded: offset, status: "uploading", error: undefined });
      } catch (error) {
        if (!(error instanceof Retryable)) {
          throw error;
        }
        const delay = RETRY_DELAYS_MS[attempt];
        if (delay === undefined) {
          job.pausedByNetwork = true;
          job.stopping = "pause";
          update(job.id, { error: "The connection was lost. It will carry on when you're back online." });
          throw new Stopped();
        }
        update(job.id, { status: "retrying" });
        await wait(job, delay);
        attempt += 1;
        inSync = job.sessionId === undefined;
      }
    }
  }, [update, wait]);

  const start = useCallback(async (job: Job) => {
    const file = job.file;
    if (!file) {
      return;
    }
    update(job.id, { status: "uploading", error: undefined });
    try {
      await (job.size === 0 ? sendEmpty(job, file) : sendChunks(job, file));
      update(job.id, { status: "done", loaded: job.size });
      jobs.current.delete(job.id);
      await Promise.all([
        refresh(),
        queryClient.invalidateQueries({ queryKey: poolQuery.queryKey }),
        queryClient.invalidateQueries({ queryKey: routingQuery.queryKey }),
      ]);
    } catch (error) {
      if (error instanceof Stopped) {
        if (job.stopping === "pause") {
          update(job.id, { status: "paused" });
        }
        return;
      }
      jobs.current.delete(job.id);
      const failure = error instanceof ApiError ? error : toApiError(error);
      update(job.id, { status: "failed", error: failure.message });
    }
  }, [queryClient, refresh, sendChunks, sendEmpty, update]);

  const pump = useCallback(() => {
    while (running.current < MAX_RUNNING) {
      const next = queue.current.shift();
      if (next === undefined) {
        return;
      }
      const job = jobs.current.get(next);
      if (job) {
        running.current += 1;
        void start(job).finally(() => {
          running.current -= 1;
          pump();
        });
      }
    }
  }, [start]);

  const enqueue = useCallback((job: Job) => {
    job.stopping = undefined;
    job.pausedByNetwork = false;
    queue.current.push(job.id);
    update(job.id, { status: "waiting", error: undefined });
    pump();
  }, [pump, update]);

  const queueFiles = useCallback((files: { file: File; folderId: string | undefined }[]) => {
    const connectionId = target || undefined;
    const added = files.map(({ file, folderId }) => {
      nextId.current += 1;
      const id = nextId.current;
      jobs.current.set(id, {
        id,
        file,
        connectionId,
        folderId,
        name: file.name,
        size: file.size,
        lastModified: file.lastModified,
      });
      queue.current.push(id);
      return { id, name: file.name, loaded: 0, total: file.size, status: "waiting" as const, connectionId };
    });
    setUploads((current) => [...current, ...added]);
    pump();
  }, [pump, target]);

  /** Waits for an answer from the dialog in the upload panel. True means skip them. */
  const askAboutDuplicates = useCallback(
    (count: number) =>
      new Promise<boolean>((resolve) => {
        setDuplicates({
          count,
          answer: (skip) => {
            setDuplicates(null);
            resolve(skip);
          },
        });
      }),
    [],
  );

  const add = useCallback((files: File[], folderId: string | undefined) => {
    queueFiles(files.map((file) => ({ file, folderId })));
  }, [queueFiles]);

  const addTree = useCallback(async (name: string, tree: FolderTree | Promise<FolderTree>, folderId: string | undefined) => {
    nextId.current += 1;
    const row = nextId.current;
    setUploads((current) => [...current, { id: row, name, loaded: 0, total: 0, status: "preparing" }]);
    try {
      const read = await tree;
      const { ids, made } = await makeFolders(queryClient, read, folderId);
      const already = await filesAlreadyThere(queryClient, read, ids, made);
      const skip = already.size > 0 && (await askAboutDuplicates(already.size));
      const wanted = skip ? read.files.filter(({ file }) => !already.has(file)) : read.files;
      if (wanted.length === 0) {
        update(row, { name: `${name}: nothing new`, status: "done" });
      } else {
        setUploads((current) => current.filter((u) => u.id !== row));
        queueFiles(wanted.map(({ file, dirs }) => ({ file, folderId: ids.get(dirs.join("/")) })));
      }
      await refresh();
    } catch (error) {
      update(row, { status: "failed", error: failureMessage(error) });
    }
  }, [askAboutDuplicates, queryClient, queueFiles, refresh, update]);

  const pause = useCallback((id: number) => {
    const job = jobs.current.get(id);
    if (!job) {
      return;
    }
    queue.current = queue.current.filter((item) => item !== id);
    job.stopping = "pause";
    update(id, { status: "paused" });
    job.request?.abort();
    job.wake?.();
  }, [update]);

  const resume = useCallback((id: number) => {
    const job = jobs.current.get(id);
    if (job?.file) {
      enqueue(job);
    }
  }, [enqueue]);

  const reattach = useCallback((id: number, file: File) => {
    const job = jobs.current.get(id);
    if (!job) {
      return;
    }
    if (file.name !== job.name || file.size !== job.size || file.lastModified !== job.lastModified) {
      update(id, { error: `That's a different file. Pick ${job.name} from where it was.` });
      return;
    }
    job.file = file;
    enqueue(job);
  }, [enqueue, update]);

  const cancel = useCallback((id: number) => {
    const job = jobs.current.get(id);
    if (!job) {
      return;
    }
    queue.current = queue.current.filter((item) => item !== id);
    job.stopping = "cancel";
    job.request?.abort();
    job.wake?.();
    jobs.current.delete(id);
    update(id, { status: "cancelled", error: undefined });
    if (job.sessionId) {
      void api
        .DELETE("/api/v1/uploads/{id}", { params: { path: { id: job.sessionId } } })
        .then(() => queryClient.invalidateQueries({ queryKey: poolQuery.queryKey }));
    }
  }, [queryClient, update]);

  const clearFinished = useCallback(() => {
    setUploads((current) => current.filter((u) => !["done", "failed", "cancelled"].includes(u.status)));
  }, []);

  // Uploads from before a reload live on the server, waiting for their file.
  useEffect(() => {
    let current = true;
    void api.GET("/api/v1/uploads").then(({ data }) => {
      if (!current || !data) {
        return;
      }
      const known = new Set([...jobs.current.values()].map((job) => job.sessionId));
      const found = data.filter((session) => !known.has(session.id)).map((session) => {
        nextId.current += 1;
        const id = nextId.current;
        jobs.current.set(id, {
          id,
          connectionId: session.connection_id,
          folderId: session.folder_id ?? undefined,
          sessionId: session.id,
          name: session.name,
          size: session.size,
          lastModified: session.last_modified,
        });
        return {
          id,
          name: session.name,
          loaded: session.received,
          total: session.size,
          status: "needs-file" as const,
          connectionId: session.connection_id,
        };
      });
      setUploads((list) => [...found, ...list]);
    });
    return () => {
      current = false;
    };
  }, []);

  // Uploads the connection stopped carry on when it comes back.
  useEffect(() => {
    function carryOn() {
      for (const job of jobs.current.values()) {
        if (job.pausedByNetwork) {
          enqueue(job);
        }
      }
    }
    window.addEventListener("online", carryOn);
    return () => window.removeEventListener("online", carryOn);
  }, [enqueue]);

  // Leaving is safe, but picking every file again isn't something to do by accident.
  const busy = uploads.some((u) => ["preparing", "uploading", "waiting", "retrying"].includes(u.status));
  useEffect(() => {
    if (!busy) {
      return undefined;
    }
    function warn(event: BeforeUnloadEvent) {
      event.preventDefault();
    }
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [busy]);

  const value = useMemo(
    () => ({ uploads, duplicates, target, setTarget, add, addTree, pause, resume, reattach, cancel, clearFinished }),
    [uploads, duplicates, target, add, addTree, pause, resume, reattach, cancel, clearFinished],
  );
  return <UploadsContext.Provider value={value}>{children}</UploadsContext.Provider>;
}
