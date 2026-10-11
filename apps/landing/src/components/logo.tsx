/** The "A" tile and wordmark, shared by nav and footer. */
export function Logo() {
  return (
    <span className="flex items-center gap-2.5">
      <span className="grid h-7 w-7 place-items-center rounded-lg bg-[linear-gradient(135deg,#F25C5C,#FB7185)] shadow-[0_4px_20px_-4px_rgba(242,92,92,0.6)]">
        <span className="font-bold" style={{ color: "#212427" }}>
          A
        </span>
      </span>
      <span className="text-[17px] font-semibold tracking-tight text-white">
        Argentum
      </span>
    </span>
  );
}
