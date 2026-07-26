import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const LINKS = [
  { href: "#features", label: "Features" },
  { href: "#integrations", label: "Integrations" },
  { href: "#how", label: "How it works" },
];

export function Nav() {
  const [scrolled, setScrolled] = useState(false);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 12);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  const handleAnchorClick = (e: React.MouseEvent<HTMLAnchorElement>, href: string) => {
    if (!href.startsWith("#")) return;
    e.preventDefault();
    if (href === "#") {
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    const el = document.querySelector(href);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <header
      className={cn(
        "fixed inset-x-0 top-0 z-50 transition-all",
        scrolled
          ? "border-b border-white/[0.06] bg-[rgba(33,36,39,0.7)] backdrop-blur-xl"
          : "border-b border-transparent",
      )}
    >
      <div className="container-page flex h-16 items-center justify-between">
        <a
          href="#"
          onClick={(e) => handleAnchorClick(e, "#")}
          className="flex items-center gap-2.5"
        >
          <span className="grid h-7 w-7 place-items-center rounded-lg bg-[linear-gradient(135deg,#F25C5C,#FB7185)] shadow-[0_4px_20px_-4px_rgba(242,92,92,0.6)]">
            <span className="font-bold text-bg" style={{ color: "#212427" }}>
              A
            </span>
          </span>
          <span className="text-base font-semibold tracking-tight">
            Argentum
          </span>
        </a>

        <nav className="hidden items-center gap-1 md:flex">
          {LINKS.map((l) => (
            <a
              key={l.href}
              href={l.href}
              onClick={(e) => handleAnchorClick(e, l.href)}
              className="rounded-full px-4 py-2 text-sm text-white/70 transition hover:bg-white/[0.05] hover:text-white"
            >
              {l.label}
            </a>
          ))}
        </nav>

        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" className="hidden sm:inline-flex">
            Sign in
          </Button>
          <Button size="sm">Start free</Button>
        </div>
      </div>
    </header>
  );
}
