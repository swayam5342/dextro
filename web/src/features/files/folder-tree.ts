/** A folder someone chose from their computer. Paths are lists of folder names. */
export interface FolderTree {
  files: { file: File; dirs: string[] }[];
  /** Every folder found, which is how an empty one survives. */
  folders: string[][];
}

/** A folder picker gives a flat list, and can't show folders that have nothing in them. */
export function treeFromPicked(files: File[]): FolderTree {
  return {
    files: files.map((file) => ({ file, dirs: file.webkitRelativePath.split("/").slice(0, -1) })),
    folders: [],
  };
}

function readBatch(reader: FileSystemDirectoryReader) {
  return new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
}

/** Browsers hand over a folder's contents in batches, until one comes back empty. */
async function readDirectory(directory: FileSystemDirectoryEntry) {
  const reader = directory.createReader();
  const entries: FileSystemEntry[] = [];
  for (;;) {
    const batch = await readBatch(reader);
    if (batch.length === 0) {
      return entries;
    }
    entries.push(...batch);
  }
}

function readFile(entry: FileSystemFileEntry) {
  return new Promise<File>((resolve, reject) => entry.file(resolve, reject));
}

async function walk(entry: FileSystemEntry, dirs: string[], tree: FolderTree) {
  if (entry.isFile) {
    tree.files.push({ file: await readFile(entry as FileSystemFileEntry), dirs });
    return;
  }
  const path = [...dirs, entry.name];
  tree.folders.push(path);
  for (const child of await readDirectory(entry as FileSystemDirectoryEntry)) {
    await walk(child, path, tree);
  }
}

/** Reads what was dropped. The entries have to be taken during the drop itself. */
export async function treeFromDrop(entries: FileSystemEntry[]): Promise<FolderTree> {
  const tree: FolderTree = { files: [], folders: [] };
  for (const entry of entries) {
    await walk(entry, [], tree);
  }
  return tree;
}

/** Each folder a tree needs, parents before children. */
export function foldersToMake(tree: FolderTree): string[][] {
  const paths = new Map<string, string[]>();
  for (const dirs of [...tree.folders, ...tree.files.map(({ dirs: fileDirs }) => fileDirs)]) {
    for (let depth = 1; depth <= dirs.length; depth += 1) {
      const path = dirs.slice(0, depth);
      paths.set(path.join("/"), path);
    }
  }
  return [...paths.values()].sort((a, b) => a.length - b.length);
}
