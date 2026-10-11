import { Logo } from "@/components/logo";
import { DEMO_LINK, DOCS_LINK, SMARTSOFT_LINK } from "@/lib/contact";

const COLS = [
  {
    title: "Product",
    links: [
      { label: "What you get", href: "#outputs" },
      { label: "Watchers", href: "#watchers" },
      { label: "Channels", href: "#channels" },
      { label: "Trust", href: "#trust" },
    ],
  },
  {
    title: "Developers",
    links: [
      { label: "API quickstart", href: DOCS_LINK },
      { label: "Examples", href: "/docs/examples/" },
      { label: "OpenAPI spec", href: "/docs/v1.yaml" },
    ],
  },
  {
    title: "Company",
    links: [
      { label: "SmartSoft", href: SMARTSOFT_LINK, external: true },
      { label: "Book a demo", href: DEMO_LINK, external: true },
    ],
  },
];

export function Footer() {
  return (
    <footer className="relative border-t border-white/[0.06] pb-10 pt-16">
      <div className="container-page">
        <div className="grid gap-12 md:grid-cols-[1.5fr_1fr_1fr_1fr]">
          <div>
            <Logo />
            <p className="mt-5 max-w-xs font-display text-[18px] leading-snug text-white/70">
              Your data, finally talkative.
            </p>
            <p className="mt-3 max-w-xs text-[13.5px] leading-relaxed text-white/45">
              The AI analyst that turns rows into answers, and answers into action.
            </p>
          </div>
          {COLS.map((c) => (
            <div key={c.title}>
              <p className="mb-4 text-[13px] font-semibold text-white/80">{c.title}</p>
              <ul className="space-y-2.5">
                {c.links.map((l) => (
                  <li key={l.label}>
                    <a
                      href={l.href}
                      {...("external" in l && l.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
                      className="text-[14px] text-white/60 transition hover:text-white"
                    >
                      {l.label}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <div className="mt-14 flex flex-col items-start justify-between gap-3 border-t border-white/[0.06] pt-6 text-xs text-white/40 sm:flex-row sm:items-center">
          <span>
            © 2026 Argentum by{" "}
            <a
              className="text-white/60 transition hover:text-white"
              href={SMARTSOFT_LINK}
              target="_blank"
              rel="noopener noreferrer"
            >
              SmartSoft
            </a>{" "}
            · PT Harapan Ananda Kamila. All rights reserved.
          </span>
          <span>Made in Jakarta, for the data-curious.</span>
        </div>
      </div>
    </footer>
  );
}
