import * as React from "react";
import { cn } from "@/lib/utils";

export const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(({ className, ...props }, ref) => (
  <input
    ref={ref}
    className={cn(
      "h-11 w-full rounded-full border border-white/10 bg-white/[0.03] px-5 text-sm text-white placeholder:text-white/40 focus:border-red-400/50 focus:outline-none focus:ring-2 focus:ring-red-400/30",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";
