import Link from "next/link";

export default function NotFound() {
  return (
    <section className="page">
      <h1>Knowledge not found</h1>
      <p>
        The core has no such knowledge object. <Link href="/knowledge">Back to knowledge</Link>
      </p>
    </section>
  );
}
