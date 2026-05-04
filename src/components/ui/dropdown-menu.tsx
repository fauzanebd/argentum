import { useState, useRef, useEffect, createContext, useContext, useCallback } from "react";
import { cn } from "@/lib/utils";

interface DropdownMenuContextValue {
  close: () => void;
}

const DropdownContext = createContext<DropdownMenuContextValue | null>(null);

interface DropdownMenuProps {
  trigger: React.ReactNode;
  children: React.ReactNode;
  align?: "left" | "right";
}

export function DropdownMenu({ trigger, children, align = "right" }: DropdownMenuProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const close = useCallback(() => setOpen(false), []);

  useEffect(() => {
    function handleMouseDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    if (open) {
      document.addEventListener("mousedown", handleMouseDown);
      return () => document.removeEventListener("mousedown", handleMouseDown);
    }
  }, [open]);

  return (
    <DropdownContext.Provider value={{ close }}>
      <div ref={ref} className="relative">
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setOpen((v) => !v);
          }}
          className="shrink-0"
        >
          {trigger}
        </button>
        {open && (
          <div
            className={cn(
              "absolute z-50 min-w-[140px] rounded-md border bg-popover text-popover-foreground shadow-md mt-1 py-1",
              align === "right" ? "right-0" : "left-0"
            )}
          >
            {children}
          </div>
        )}
      </div>
    </DropdownContext.Provider>
  );
}

interface DropdownMenuItemProps {
  children: React.ReactNode;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  className?: string;
  destructive?: boolean;
}

export function DropdownMenuItem({ children, onClick, className, destructive }: DropdownMenuItemProps) {
  const ctx = useContext(DropdownContext);

  const handleClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    onClick?.(e);
    ctx?.close();
  };

  return (
    <button
      type="button"
      onClick={handleClick}
      className={cn(
        "w-full px-3 py-2 text-sm flex items-center gap-2 hover:bg-accent hover:text-accent-foreground transition-colors",
        destructive && "text-destructive hover:bg-destructive/10 hover:text-destructive",
        className
      )}
    >
      {children}
    </button>
  );
}
