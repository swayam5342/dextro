import {
  File as FileIcon,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  type LucideIcon,
} from "lucide-react";

const ARCHIVES = ["zip", "gzip", "x-tar", "x-7z", "x-rar"];
const SHEETS = ["spreadsheet", "excel", "csv"];
const CODE = ["json", "javascript", "xml", "yaml", "html"];

function includesAny(text: string, words: string[]): boolean {
  return words.some((word) => text.includes(word));
}

/** The icon for a file that has no picture of its own. */
export function fileIcon(contentType: string): LucideIcon {
  if (contentType.startsWith("image/")) {
    return FileImage;
  }
  if (contentType.startsWith("video/")) {
    return FileVideo;
  }
  if (contentType.startsWith("audio/")) {
    return FileAudio;
  }
  if (includesAny(contentType, ARCHIVES)) {
    return FileArchive;
  }
  if (includesAny(contentType, SHEETS)) {
    return FileSpreadsheet;
  }
  if (includesAny(contentType, CODE)) {
    return FileCode;
  }
  if (contentType.startsWith("text/") || contentType.includes("pdf") || contentType.includes("document")) {
    return FileText;
  }
  return FileIcon;
}

/** Files are where the colour goes, in the same colours the usage card uses for each kind. */
export function fileTint(contentType: string): string {
  if (contentType.startsWith("image/")) {
    return "text-usage-photo";
  }
  if (contentType.startsWith("video/")) {
    return "text-usage-video";
  }
  if (includesAny(contentType, ARCHIVES)) {
    return "text-usage-free";
  }
  if (contentType.startsWith("audio/") || includesAny(contentType, CODE)) {
    return "text-usage-other";
  }
  if (includesAny(contentType, SHEETS)) {
    return "text-usage-photo";
  }
  if (contentType.startsWith("text/") || contentType.includes("pdf") || contentType.includes("word") || contentType.includes("document")) {
    return "text-usage-document";
  }
  return "text-muted-foreground";
}
