import { useEffect, useState } from "react";
import { Menu, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/logo";
import { DEMO_LINK, DOCS_LINK } from "@/lib/contact";
import { cn } from "@/lib/utils";

// `/docs/` is a real navigation, not an anchor — the browser follows it to
// the static pages `scripts/build-docs.mjs` publishes.
const LINKS = [
  { href: "#outputs", label: "Product" },
  { href: "#watchers", label: "Watchers" },
  { href: "#channels", label: "Channels" },
  { href: "#trust", label: "Trust" },
  { href: "#developers", label: "Developers" },
  { href: DOCS_LINK, label: "Docs" },
];

export function Nav() {
  const [scrolled, setScrolled] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 12);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  const go = (e: React.MouseEvent<HTMLAnchorElement>, href: string) => {
    setOpen(false);
    if (!href.startsWith("#")) return;
    e.preventDefault();
    if (href === "#") {
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    document.querySelector(href)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <header
      className={cn(
        "fixed inset-x-0 top-0 z-50 transition-all duration-300",
        scrolled || open
          ? "border-b border-white/[0.06] bg-[rgba(22,24,26,0.78)] backdrop-blur-xl"
          : "border-b border-transparent",
      )}
    >
      <div className="container-page flex h-16 items-center justify-between">
        <a href="#" onClick={(e) => go(e, "#")} aria-label="Argentum, back to top">
          <Logo />
        </a>

        <nav className="hidden items-center gap-1 lg:flex">
          {LINKS.map((l) => (
            <a
              key={l.href}
              href={l.href}
              onClick={(e) => go(e, l.href)}
              className="rounded-full px-3.5 py-2 text-[14px] text-white/65 transition hover:text-white"
            >
              {l.label}
            </a>
          ))}
        </nav>

        <div className="flex items-center gap-2">
          <Button asChild size="sm" className="hidden sm:inline-flex">
            <a href={DEMO_LINK} target="_blank" rel="noopener noreferrer">
              Book a demo
            </a>
          </Button>
          <button
            type="button"
            aria-label={open ? "Close menu" : "Open menu"}
            aria-expanded={open}
            onClick={() => setOpen((v) => !v)}
            className="grid h-10 w-10 place-items-center rounded-full text-white/80 hover:bg-white/[0.06] lg:hidden"
          >
            {open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
          </button>
        </div>
      </div>

      {open && (
        <nav className="container-page flex flex-col gap-1 pb-5 lg:hidden">
          {LINKS.map((l) => (
            <a
              key={l.href}
              href={l.href}
              onClick={(e) => go(e, l.href)}
              className="rounded-lg px-2 py-2.5 text-[15px] text-white/75 hover:bg-white/[0.04] hover:text-white"
            >
              {l.label}
            </a>
          ))}
          <Button asChild className="mt-2 sm:hidden">
            <a href={DEMO_LINK} target="_blank" rel="noopener noreferrer">
              Book a demo
            </a>
          </Button>
        </nav>
      )}
    </header>
  );
}
