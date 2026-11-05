// What to create and upload, with paths relative to the open folder. Folders
// come shallowest first, so each parent exists before its children.
export type UploadPlan = {
  dirs: string[];
  files: { path: string; file: File }[];
};

function finish(dirs: Set<string>, files: UploadPlan['files']): UploadPlan {
  const depth = (value: string) => value.split('/').length;
  return {
    dirs: [...dirs].sort((a, b) => depth(a) - depth(b) || a.localeCompare(b)),
    files
  };
}

// Files from the picker. A folder picker gives each file a relative path that
// starts with the picked folder; a plain file picker gives none.
export function planFromFiles(list: File[]): UploadPlan {
  const dirs = new Set<string>();
  const files = list.map((file) => {
    const path = file.webkitRelativePath || file.name;
    const parts = path.split('/');
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/'));
    return { path, file };
  });
  return finish(dirs, files);
}

// readEntries returns at most 100 entries per call in Chrome: read until empty.
async function readAll(reader: FileSystemDirectoryReader, signal: AbortSignal) {
  const all: FileSystemEntry[] = [];
  for (;;) {
    signal.throwIfAborted();
    const batch = await new Promise<FileSystemEntry[]>((resolve, reject) =>
      reader.readEntries(resolve, reject)
    );
    signal.throwIfAborted();
    if (!batch.length) return all;
    all.push(...batch);
  }
}

async function walk(
  entry: FileSystemEntry,
  prefix: string,
  dirs: Set<string>,
  files: UploadPlan['files'],
  signal: AbortSignal
) {
  signal.throwIfAborted();
  const path = prefix ? prefix + '/' + entry.name : entry.name;
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) =>
      (entry as FileSystemFileEntry).file(resolve, reject)
    );
    signal.throwIfAborted();
    files.push({ path, file });
  } else if (entry.isDirectory) {
    // Empty folders are created too.
    dirs.add(path);
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    for (const child of await readAll(reader, signal)) {
      await walk(child, path, dirs, files, signal);
    }
  }
}

// The entries must be taken from the drop event before any await: the
// browser empties dataTransfer once the handler returns.
export function dropEntries(data: DataTransfer) {
  const entries: FileSystemEntry[] = [];
  for (const item of Array.from(data.items)) {
    const entry = item.kind === 'file' ? item.webkitGetAsEntry() : null;
    if (entry) entries.push(entry);
  }
  return { entries, files: Array.from(data.files) };
}

export async function planFromDrop(
  { entries, files }: ReturnType<typeof dropEntries>,
  signal: AbortSignal
) {
  signal.throwIfAborted();
  if (!entries.length) return planFromFiles(files);
  const dirs = new Set<string>();
  const planned: UploadPlan['files'] = [];
  for (const entry of entries) await walk(entry, '', dirs, planned, signal);
  return finish(dirs, planned);
}
