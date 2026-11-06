import type { ReactNode } from "react";

import { Mark } from "@/components/mark";

/** The frame for pages shown outside the app: signing in, and errors. */
export function CenteredPage({ children }: { children: ReactNode }) {
  return (
    <div className="grid min-h-full place-items-center p-4">
      <div className="w-full max-w-sm space-y-6 py-8">
        <p className="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
          <Mark className="size-7" />
          Tributary
        </p>
        {children}
      </div>
    </div>
  );
}
