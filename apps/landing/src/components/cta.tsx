import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Reveal } from "@/components/reveal";
import { DEMO_LINK } from "@/lib/contact";

export function CTA() {
  return (
    <section className="relative py-24 sm:py-32">
      <div className="container-page">
        <Reveal>
          <div className="gradient-border relative overflow-hidden rounded-[2rem] bg-[#141618] px-6 py-16 sm:px-14 sm:py-24">
            <div aria-hidden className="ledger-bg absolute inset-0 opacity-70" />
            <div aria-hidden className="absolute -right-24 -top-24 h-[420px] w-[420px] rounded-full bg-[radial-gradient(circle,rgba(242,92,92,0.28),transparent_65%)] blur-2xl" />

            <div className="relative grid gap-10 lg:grid-cols-[1.4fr_1fr] lg:items-end">
              <div>
                <h2 className="text-balance font-display text-[36px] leading-[1.06] text-white sm:text-[50px] md:text-[60px]">
                  Stop digging through dashboards.{" "}
                  <em className="signal-text italic">Start asking.</em>
                </h2>
                <p className="mt-6 max-w-lg text-[16.5px] leading-relaxed text-white/60">
                  Bring your messiest database. On a call, we connect it read-only and ask
                  it the questions you've been putting off.
                </p>
              </div>
              <div className="flex flex-col gap-3 sm:flex-row lg:flex-col lg:items-stretch">
                <Button asChild size="lg">
                  <a href={DEMO_LINK} target="_blank" rel="noopener noreferrer">
                    Book a demo on WhatsApp <ArrowRight className="h-4 w-4" />
                  </a>
                </Button>
              </div>
            </div>
          </div>
        </Reveal>
      </div>
    </section>
  );
}
