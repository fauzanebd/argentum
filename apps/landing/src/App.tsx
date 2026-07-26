import { CTA } from "@/components/cta";
import { Features } from "@/components/features";
import { Footer } from "@/components/footer";
import { GradientBg } from "@/components/gradient-bg";
import { Hero } from "@/components/hero";
import { HowItWorks } from "@/components/how-it-works";
import { Integrations } from "@/components/integrations";
import { Nav } from "@/components/nav";
import { UseCases } from "@/components/use-cases";

export default function App() {
  return (
    <>
      <GradientBg />
      <Nav />
      <main>
        <Hero />
        <Features />
        <Integrations />
        <HowItWorks />
        <UseCases />
        <CTA />
      </main>
      <Footer />
    </>
  );
}
