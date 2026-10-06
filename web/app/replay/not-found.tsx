import Link from "next/link";

export default function NotFound() {
  return (
    <section className="page">
      <h1>Manifest not found</h1>
      <p>
        The core has no such demo manifest. <Link href="/replay">Back to replay</Link>
      </p>
    </section>
  );
}
