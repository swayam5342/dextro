import { X } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

const VISIBLE_MS = 6000;

export interface ToastInput {
  message: string;
  action?: { label: string; run: () => void };
}

type ShowToast = (toast: ToastInput) => void;

const ToastContext = createContext<ShowToast | null>(null);

export function useToast(): ShowToast {
  const show = useContext(ToastContext);
  if (!show) {
    throw new Error("useToast needs a ToastProvider");
  }
  return show;
}

/**
 * One short message at a time, and a newer one replaces it. It waits while the
 * pointer or keyboard is on it, so an Undo can't vanish as someone reaches for it.
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<(ToastInput & { id: number }) | null>(null);
  const [held, setHeld] = useState(false);
  const nextId = useRef(0);

  const show = useCallback<ShowToast>((input) => {
    nextId.current += 1;
    setHeld(false);
    setToast({ ...input, id: nextId.current });
  }, []);

  const dismiss = useCallback(() => {
    setHeld(false);
    setToast(null);
  }, []);

  useEffect(() => {
    if (!toast || held) {
      return undefined;
    }
    const timer = setTimeout(dismiss, VISIBLE_MS);
    return () => clearTimeout(timer);
  }, [toast, held, dismiss]);

  const value = useMemo(() => show, [show]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      {/* Above the viewer, and clickable while a dialog has switched the page's pointer events off. */}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed inset-x-4 bottom-[74px] z-[60] flex justify-center md:bottom-5"
      >
        {toast && (
          <div
            key={toast.id}
            data-toast
            onMouseEnter={() => setHeld(true)}
            onMouseLeave={() => setHeld(false)}
            onFocus={() => setHeld(true)}
            onBlur={() => setHeld(false)}
            className="pointer-events-auto flex max-w-full items-center gap-1 rounded-2xl bg-raised py-1.5 pr-1.5 pl-4 text-sm shadow-[0_12px_32px_rgba(0,0,0,0.7)]"
          >
            <span className="min-w-0 py-1 pr-2">{toast.message}</span>
            {toast.action && (
              <button
                type="button"
                className="shrink-0 rounded-full px-3 py-1 font-semibold hover:bg-[#3a3a3a]"
                onClick={() => {
                  dismiss();
                  toast.action?.run();
                }}
              >
                {toast.action.label}
              </button>
            )}
            <button
              type="button"
              aria-label="Dismiss"
              className="grid size-7 shrink-0 place-items-center rounded-full text-muted-foreground hover:bg-[#3a3a3a] hover:text-foreground"
              onClick={dismiss}
            >
              <X className="size-4" />
            </button>
          </div>
        )}
      </div>
    </ToastContext.Provider>
  );
}
