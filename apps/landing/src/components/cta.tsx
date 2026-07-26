import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";

export function CTA() {
  return (
    <section className="py-24 sm:py-32">
      <div className="container-page">
        <div className="glass-strong gradient-border relative overflow-hidden rounded-3xl px-8 py-16 text-center sm:px-16 sm:py-24">
          <div className="pointer-events-none absolute -top-32 left-1/2 h-[420px] w-[420px] -translate-x-1/2 rounded-full bg-red-500/30 blur-3xl" />
          <div className="pointer-events-none absolute -bottom-32 right-0 h-[320px] w-[320px] rounded-full bg-rose-400/20 blur-3xl" />

          <div className="relative">
            <h2 className="mx-auto max-w-3xl text-3xl font-bold tracking-tight sm:text-5xl">
              Stop digging through dashboards.
              <br />
              <span className="gradient-text">Start asking.</span>
            </h2>
            <p className="mx-auto mt-5 max-w-xl text-base text-white/65">
              Join the early-access list. We onboard new teams every Monday — bring your messiest database.
            </p>

            <form
              className="mx-auto mt-8 flex max-w-md flex-col gap-2 sm:flex-row"
              onSubmit={(e) => e.preventDefault()}
            >
              <input
                type="email"
                required
                placeholder="you@company.com"
                className="h-12 flex-1 rounded-full border border-white/10 bg-white/[0.04] px-5 text-sm text-white placeholder:text-white/40 focus:border-red-400/50 focus:outline-none focus:ring-2 focus:ring-red-400/30"
              />
              <Button size="lg" type="submit">
                Get early access
                <ArrowRight className="h-4 w-4" />
              </Button>
            </form>
            <p className="mt-3 text-xs text-white/35">
              We email once. No newsletter, no nonsense.
            </p>
          </div>
        </div>
      </div>
    </section>
  );
}
