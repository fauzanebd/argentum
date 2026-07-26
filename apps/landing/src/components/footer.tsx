export function Footer() {
  return (
    <footer className="border-t border-white/[0.06] py-14">
      <div className="container-page">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="grid h-7 w-7 place-items-center rounded-lg bg-[linear-gradient(135deg,#F25C5C,#FB7185)]">
              <span className="font-bold" style={{ color: "#212427" }}>
                A
              </span>
            </span>
            <span className="font-semibold">Argentum</span>
          </div>
          <p className="mt-4 max-w-sm text-sm text-white/55">
            Your data, finally talkative. The AI agent that turns rows into
            answers and answers into action.
          </p>
        </div>

        <div className="mt-12 flex flex-col items-start justify-between gap-3 border-t border-white/[0.06] pt-6 text-xs text-white/40 sm:flex-row sm:items-center">
          <span>© 2026 Argentum by Smartsoft, Inc. All rights reserved.</span>
          <span className="font-mono">
            Built with care · Made for the data-curious.
          </span>
        </div>
      </div>
    </footer>
  );
}
