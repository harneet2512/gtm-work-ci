/** The Ghost mark: a small ghost in an ink tile. Decorative; the wordmark next to it carries the name. */
export function BrandMark() {
  return (
    <span className="brand-mark">
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
        <path
          d="M8 1.75c-2.9 0-5 2.2-5 5.1v6.6c0 .5.55.78.95.48l1.1-.82 1.12.85c.27.2.64.2.9 0L8 13.1l.93.86c.27.2.64.2.9 0l1.12-.85 1.1.82c.4.3.95.02.95-.48V6.85c0-2.9-2.1-5.1-5-5.1Z"
          fill="currentColor"
        />
        <circle cx="6.1" cy="7" r="1" fill="var(--ink)" />
        <circle cx="9.9" cy="7" r="1" fill="var(--ink)" />
      </svg>
    </span>
  );
}
