import Link from "next/link";

export default function NotFound() {
  return (
    <section className="page">
      <h1>Account not found</h1>
      <p>
        The core has no such account. <Link href="/">Back to accounts</Link>
      </p>
    </section>
  );
}
