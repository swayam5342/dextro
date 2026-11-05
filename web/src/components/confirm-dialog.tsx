import { FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";

interface ConfirmDialogProps {
  title: string;
  text: string;
  action: string;
  pending?: boolean;
  error?: string;
  onConfirm: () => void;
  onClose: () => void;
}

/** Asks before something that can't be undone, naming what's about to happen. */
export function ConfirmDialog({ title, text, action, pending, error, onConfirm, onClose }: ConfirmDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <div className="space-y-1.5">
          <DialogTitle className="text-base font-semibold">{title}</DialogTitle>
          <DialogDescription className="text-sm text-muted-foreground">{text}</DialogDescription>
        </div>
        <FormError message={error} />
        <div className="flex justify-end gap-2">
          <DialogClose asChild>
            <Button variant="outline" className="bg-raised hover:bg-[#3a3a3a]">
              Cancel
            </Button>
          </DialogClose>
          <Button variant="destructive" disabled={pending} onClick={onConfirm}>
            {action}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
