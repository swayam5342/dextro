import { useState, type HTMLAttributes } from "react";

import { failureMessage } from "@/api/errors";
import type { components } from "@/api/schema";
import { useToast } from "@/components/toast";
import { count } from "@/lib/format";

import { itemKey, useMove, type Item } from "./queries";

type File = components["schemas"]["File"];
type Folder = components["schemas"]["Folder"];

/** Something being dragged, and the folder to put it back in. */
interface Moving {
  item: Item;
  from: string | null;
}

/** Has no "Files" type, so the upload drop zone leaves these drags alone. */
const DRAG_TYPE = "application/x-dextro-items";
export const TOP_LEVEL = "top";

/** Shows how many are moving, because a browser only pictures the one that was grabbed. */
function setCountPicture(transfer: DataTransfer, items: number) {
  const picture = document.createElement("div");
  picture.textContent = `${items} items`;
  picture.className = "fixed -top-24 left-0 rounded-full bg-raised px-3 py-1.5 text-sm font-medium text-foreground";
  document.body.append(picture);
  transfer.setDragImage(picture, 0, 0);
  setTimeout(() => picture.remove());
}

/** Dragging items onto folders and the path, with Undo. */
export function useDragMove({
  selected,
  chosen,
  folders,
  files,
  path,
  deselect,
}: {
  selected: Item[];
  chosen: Set<string>;
  folders: Folder[];
  files: File[];
  path: Folder[];
  deselect: (items: Item[]) => void;
}) {
  const [dragging, setDragging] = useState<Moving[] | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const move = useMove();
  const toast = useToast();

  function originOf(item: Item): string | null {
    if (item.kind === "file") {
      return files.find(({ id }) => id === item.id)?.folder_id ?? null;
    }
    return folders.find(({ id }) => id === item.id)?.parent_id ?? null;
  }

  function placeName(target: string | null): string {
    const places = [...folders, ...path];
    return places.find(({ id }) => id === target)?.name ?? "Files";
  }

  async function moveBack(done: Moving[]) {
    try {
      for (const { item, from } of done) {
        await move.mutateAsync({ item, target: from });
      }
      toast({ message: `Moved ${count(done.length, "item")} back` });
    } catch (error) {
      toast({ message: failureMessage(error) });
    }
  }

  /** One at a time, like the trash. Whatever moved before a failure can still be undone. */
  async function moveTo(target: string | null) {
    const moving = (dragging ?? []).filter(({ from }) => from !== target);
    setDragging(null);
    setDropTarget(null);
    const done: Moving[] = [];
    let failure: string | undefined;
    try {
      for (const entry of moving) {
        await move.mutateAsync({ item: entry.item, target });
        done.push(entry);
      }
    } catch (error) {
      failure = failureMessage(error);
    }
    deselect(done.map(({ item }) => item));
    const undo = { label: "Undo", run: () => void moveBack(done) };
    toast({
      message: failure ?? `Moved ${count(done.length, "item")} to ${placeName(target)}`,
      action: done.length > 0 ? undo : undefined,
    });
  }

  /** Dragging a selected item takes the whole selection with it. */
  function dragFrom(item: Item): HTMLAttributes<HTMLElement> {
    return {
      draggable: true,
      onDragStart(event) {
        const items = chosen.has(itemKey(item)) ? selected : [item];
        const moving = items.map((one) => ({ item: one, from: originOf(one) }));
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData(DRAG_TYPE, "items");
        if (items.length > 1) {
          setCountPicture(event.dataTransfer, items.length);
        }
        // After the event, because changing the page during it can cancel the drag.
        setTimeout(() => setDragging(moving));
      },
      onDragEnd() {
        setDragging(null);
        setDropTarget(null);
      },
    };
  }

  /** Only offers a place the drag can actually go. */
  function dropOn(target: string | null): HTMLAttributes<HTMLElement> {
    const intoItself = dragging?.some(({ item }) => item.kind === "folder" && item.id === target);
    if (!dragging || intoItself || dragging.every(({ from }) => from === target)) {
      return {};
    }
    return {
      onDragOver(event) {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        setDropTarget(target ?? TOP_LEVEL);
      },
      onDragLeave(event) {
        if (!(event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget))) {
          setDropTarget(null);
        }
      },
      onDrop(event) {
        event.preventDefault();
        void moveTo(target);
      },
    };
  }

  return { dragging, dropTarget, dragFrom, dropOn };
}
