import * as React from "react"
import { ContextMenu as ContextMenuPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

const ContextMenu = ContextMenuPrimitive.Root
const ContextMenuTrigger = ContextMenuPrimitive.Trigger

function ContextMenuContent({
  className,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Content>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        data-slot="context-menu-content"
        className={cn(
          "z-50 min-w-52 rounded-xl bg-popover p-1.5 shadow-menu",
          className
        )}
        {...props}
      />
    </ContextMenuPrimitive.Portal>
  )
}

function ContextMenuItem({
  className,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Item>) {
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      className={cn(
        "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm outline-none select-none focus:bg-raised-hover [&_svg]:size-[18px] [&_svg]:shrink-0",
        className
      )}
      {...props}
    />
  )
}

export { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger }
