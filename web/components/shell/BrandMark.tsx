/** The gtm_ai mark: three linked nodes in an ink tile (an account and what it connects to). Decorative; the wordmark next to it carries the name. */
export function BrandMark() {
  return (
    <span className="brand-mark">
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
        <path d="M5 11 8 4.5 11 11Z" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
        <circle cx="8" cy="4.5" r="2" fill="currentColor" />
        <circle cx="5" cy="11" r="1.7" fill="currentColor" />
        <circle cx="11" cy="11" r="1.7" fill="currentColor" />
      </svg>
    </span>
  );
}
