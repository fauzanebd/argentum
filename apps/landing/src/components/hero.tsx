import { ArrowRight, Sparkles } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ChatDemo } from "@/components/chat-demo";

export function Hero() {
  return (
    <section className="relative pt-28 pb-20 sm:pt-36 sm:pb-28">
      <div className="absolute inset-0 -z-10 grid-bg opacity-60" />
      <div className="container-page">
        <div className="mx-auto max-w-3xl text-center">
          <Badge>
            <Sparkles className="h-3 w-3 text-red-300" />
            <span>AI agent for your data</span>
          </Badge>
          <h1 className="mt-6 text-5xl font-bold tracking-tight sm:text-6xl md:text-7xl">
            Talk to your data.
            <br />
            <span className="gradient-text">Ship insights in seconds.</span>
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-base text-white/65 sm:text-lg">
            Argentum reads your database, answers in plain language, and delivers
            sophisticated reports to web, WhatsApp, and Telegram — wherever your team
            already lives. Your analyst that never sleeps, never bills overtime.
          </p>
          <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
            <Button size="lg">
              Start free
              <ArrowRight className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="lg">
              Book a demo
            </Button>
          </div>
        </div>

        <div className="mt-16 sm:mt-20">
          <ChatDemo />
          <p className="mt-4 text-center text-xs text-white/40">
            ↑ Live demo on a sample sales dataset. Try a suggestion or ask anything.
          </p>
        </div>
      </div>
    </section>
  );
}
