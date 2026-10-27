// The Torrent Drive mark: peers join into one download that lands in a drive.
// It uses currentColor, so it follows the text color in both themes.
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="6 5 28 28" aria-hidden="true" className={className}>
      <g fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5">
        <path d="M12 10.5 20 16.5m8-6L20 16.5M20 10v14.5m-4.5-4.5 4.5 4.5 4.5-4.5" />
        <path d="M11 27.5v1.5a2.5 2.5 0 0 0 2.5 2.5h13A2.5 2.5 0 0 0 29 29v-1.5" />
      </g>
      <g fill="currentColor">
        <circle cx="12" cy="10" r="2.75" />
        <circle cx="20" cy="9" r="2.75" />
        <circle cx="28" cy="10" r="2.75" />
      </g>
    </svg>
  );
}
