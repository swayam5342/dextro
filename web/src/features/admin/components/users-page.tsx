import { useQuery } from "@tanstack/react-query";
import { KeyRound, MoreVertical, Trash2, UserPlus } from "lucide-react";
import { useState, type FormEvent } from "react";

import type { components } from "@/api/schema";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { meQuery } from "@/features/auth/queries";
import { formatDate } from "@/lib/format";

import { useCreateUser, useDeleteUser, useResetUserPassword, usersQuery } from "../queries";

type ManagedUser = components["schemas"]["ManagedUser"];

type Action = { type: "add" } | { type: "password"; user: ManagedUser } | { type: "delete"; user: ManagedUser };

const TEMPORARY_HINT = "At least 12 characters. Give it to them yourself; they replace it when they sign in.";

function Cancel() {
  return (
    <DialogClose asChild>
      <Button type="button" variant="soft">
        Cancel
      </Button>
    </DialogClose>
  );
}

function AddUserDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateUser();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    create.mutate({ email, password }, { onSuccess: onClose });
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle className="text-base font-semibold">Add a user</DialogTitle>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field
            id="new-user-email"
            label="Email"
            type="email"
            className="bg-raised"
            autoComplete="off"
            required
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
          <Field
            id="new-user-password"
            label="Temporary password"
            type="text"
            className="bg-raised"
            autoComplete="off"
            hint={TEMPORARY_HINT}
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          <FormError message={create.error?.message} />
          <div className="flex justify-end gap-2">
            <Cancel />
            <Button type="submit" disabled={create.isPending}>
              Add user
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function SetPasswordDialog({ user, onClose }: { user: ManagedUser; onClose: () => void }) {
  const reset = useResetUserPassword();
  const [password, setPassword] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    reset.mutate({ id: user.id, password }, { onSuccess: onClose });
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <div className="space-y-1.5">
          <DialogTitle className="truncate text-base font-semibold">Set a password for {user.email}</DialogTitle>
          <DialogDescription className="text-sm text-muted-foreground">
            They'll be signed out everywhere, and their API keys stop working until they choose a new password.
          </DialogDescription>
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field
            id="reset-password"
            label="Temporary password"
            type="text"
            className="bg-raised"
            autoComplete="off"
            hint={TEMPORARY_HINT}
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          <FormError message={reset.error?.message} />
          <div className="flex justify-end gap-2">
            <Cancel />
            <Button type="submit" disabled={reset.isPending}>
              Set password
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DeleteUserDialog({ user, onClose }: { user: ManagedUser; onClose: () => void }) {
  const remove = useDeleteUser();
  const [typed, setTyped] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    remove.mutate(user.id, { onSuccess: onClose });
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <div className="space-y-1.5">
          <DialogTitle className="truncate text-base font-semibold">Delete {user.email}?</DialogTitle>
          <DialogDescription className="text-sm text-muted-foreground">
            Their linked accounts and Tributary's records of their files go for good. The files themselves stay in
            their Google Drive. This can't be undone. Any Google links they made keep working, and Tributary can't end them afterwards.
          </DialogDescription>
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Field
            id="delete-confirm"
            label="Type their email to confirm"
            className="bg-raised"
            autoComplete="off"
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
          />
          <FormError message={remove.error?.message} />
          <div className="flex justify-end gap-2">
            <Cancel />
            <Button type="submit" variant="destructive" disabled={remove.isPending || typed.trim().toLowerCase() !== user.email}>
              Delete user
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function statusText(user: ManagedUser): string {
  if (user.must_change_password) {
    return "Hasn't chosen a password yet";
  }
  if (user.linked_accounts === 0) {
    return "No accounts linked";
  }
  return user.linked_accounts === 1 ? "1 account linked" : `${user.linked_accounts} accounts linked`;
}

function UserRow({ user, isMe, onAction }: { user: ManagedUser; isMe: boolean; onAction: (action: Action) => void }) {
  const cell = "border-t px-3 py-3 whitespace-nowrap text-muted-foreground";
  return (
    <tr>
      <td className={`${cell} w-full max-w-0 text-foreground`}>
        <span className="block truncate font-medium">{user.email}</span>
        <span className="block truncate text-xs text-faint md:hidden">{statusText(user)}</span>
      </td>
      <td className={cell}>{user.role === "admin" ? "Admin" : "Member"}</td>
      <td className={`${cell} hidden md:table-cell`}>{statusText(user)}</td>
      <td className={`${cell} hidden sm:table-cell`}>{formatDate(user.created_at)}</td>
      <td className={`${cell} w-[46px] px-1`}>
        {isMe ? (
          <span className="block w-[30px] text-center text-xs text-faint">You</span>
        ) : (
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label={`Actions for ${user.email}`}
              className="grid size-[30px] place-items-center rounded-full hover:bg-raised hover:text-foreground"
            >
              <MoreVertical className="size-[18px]" />
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => onAction({ type: "password", user })}>
                <KeyRound />
                Set a password
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAction({ type: "delete", user })}>
                <Trash2 />
                Delete user
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </td>
    </tr>
  );
}

export function UsersPage() {
  const { data: users, isPending, isError } = useQuery(usersQuery);
  const { data: me } = useQuery(meQuery);
  const [action, setAction] = useState<Action | null>(null);
  const close = () => setAction(null);

  return (
    <div className="max-w-4xl">
      <div className="mb-[18px] flex min-h-9 items-center justify-between gap-4">
        <h1 className="text-xl font-semibold tracking-tight">Users</h1>
        <Button onClick={() => setAction({ type: "add" })}>
          <UserPlus />
          Add user
        </Button>
      </div>
      {isPending && <p className="text-muted-foreground">Loading…</p>}
      {isError && <FormError message="Users couldn't be loaded. Reload the page to try again." />}
      {users && (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="text-left text-xs text-faint">
              <th className="px-3 pb-2 font-normal">Email</th>
              <th className="px-3 pb-2 font-normal">Role</th>
              <th className="hidden px-3 pb-2 font-normal md:table-cell">Status</th>
              <th className="hidden px-3 pb-2 font-normal sm:table-cell">Joined</th>
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <UserRow key={user.id} user={user} isMe={user.id === me?.id} onAction={setAction} />
            ))}
          </tbody>
        </table>
      )}
      {action?.type === "add" && <AddUserDialog onClose={close} />}
      {action?.type === "password" && <SetPasswordDialog user={action.user} onClose={close} />}
      {action?.type === "delete" && <DeleteUserDialog user={action.user} onClose={close} />}
    </div>
  );
}
