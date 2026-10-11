import type { ReactNode } from "react";
import { Reveal } from "@/components/reveal";
import { cn } from "@/lib/utils";

export function SectionHeading({
  title,
  description,
  align = "left",
  compact = false,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  align?: "left" | "center";
  /** smaller title, for headings that share a row with a visual */
  compact?: boolean;
  className?: string;
}) {
  return (
    <Reveal
      className={cn(
        align === "center" ? "mx-auto max-w-4xl text-center" : "max-w-3xl",
        className,
      )}
    >
      <h2
        className={cn(
          "text-balance font-display text-white",
          compact
            ? "text-[32px] sm:text-[38px] md:text-[44px]"
            : "text-[34px] sm:text-[44px] md:text-[54px]",
          // after the size: tailwind-merge drops a leading-* that precedes a text-* size
          "leading-[1.08]",
        )}
      >
        {title}
      </h2>
      {description && (
        <p
          className={cn(
            "mt-5 max-w-xl text-[16.5px] leading-relaxed text-white/60",
            align === "center" && "mx-auto",
          )}
        >
          {description}
        </p>
      )}
    </Reveal>
  );
}
