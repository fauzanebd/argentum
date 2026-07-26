export function GradientBg() {
  return (
    <div
      aria-hidden
      className="pointer-events-none fixed inset-0 -z-10 overflow-hidden"
    >
      <div className="absolute -top-32 left-1/2 h-[640px] w-[640px] -translate-x-1/2 rounded-full bg-red-500/20 blur-3xl animate-blob" />
      <div className="absolute top-40 right-[-10%] h-[520px] w-[520px] rounded-full bg-rose-400/15 blur-3xl animate-blob [animation-delay:-6s]" />
      <div className="absolute bottom-[-20%] left-[-10%] h-[580px] w-[580px] rounded-full bg-red-600/10 blur-3xl animate-blob [animation-delay:-12s]" />
    </div>
  );
}
