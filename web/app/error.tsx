"use client";

export default function ErrorPage({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  // The server-side message is replaced by a digest in production builds; the core's error is in the server log.
  return (
    <section className="page" role="alert">
      <h1>The core could not be read</h1>
      <p>{process.env.NODE_ENV === "production" ? "The core API did not answer this request." : error.message}</p>
      {error.digest ? <p className="hint">digest {error.digest}</p> : null}
      <button type="button" onClick={reset}>
        Try again
      </button>
    </section>
  );
}
