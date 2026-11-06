import { cn } from "@/lib/utils";

/** One option among several, which stays lit once it's picked. */
export function Choice({
  chosen,
  onClick,
  children,
  className,
}: {
  chosen: boolean;
  onClick: () => void;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <button type="button" aria-pressed={chosen} onClick={onClick} className={cn("hover:bg-raised", chosen && "bg-raised", className)}>
      {children}
    </button>
  );
}
