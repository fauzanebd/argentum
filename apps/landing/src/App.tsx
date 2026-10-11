import { Capabilities } from "@/components/capabilities";
import { Channels } from "@/components/channels";
import { CTA } from "@/components/cta";
import { Developers } from "@/components/developers";
import { Footer } from "@/components/footer";
import { Hero } from "@/components/hero";
import { HowItWorks } from "@/components/how-it-works";
import { Nav } from "@/components/nav";
import { Outputs } from "@/components/outputs";
import { Trust } from "@/components/trust";
import { Watchers } from "@/components/watchers";

export default function App() {
  return (
    <>
      <div aria-hidden className="grain" />
      <Nav />
      <main>
        <Hero />
        <Outputs />
        <Watchers />
        <Channels />
        <Trust />
        <Capabilities />
        <Developers />
        <HowItWorks />
        <CTA />
      </main>
      <Footer />
    </>
  );
}
