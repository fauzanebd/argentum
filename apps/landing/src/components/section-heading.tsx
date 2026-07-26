import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";

export function SectionHeading({
  eyebrow,
  title,
  highlight,
  description,
}: {
  eyebrow: string;
  title: string;
  highlight?: string;
  description?: ReactNode;
}) {
  return (
    <div className="mx-auto max-w-2xl text-center">
      <Badge>{eyebrow}</Badge>
      <h2 className="mt-5 text-3xl font-bold tracking-tight sm:text-4xl md:text-5xl">
        {title}{" "}
        {highlight && <span className="gradient-text">{highlight}</span>}
      </h2>
      {description && (
        <p className="mx-auto mt-5 max-w-xl text-base text-white/60">
          {description}
        </p>
      )}
    </div>
  );
}
