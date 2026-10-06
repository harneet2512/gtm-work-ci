import Link from "next/link";

export default function NotFound() {
  return (
    <section className="page">
      <h1>Run not found</h1>
      <p>
        The core has no such agent run. <Link href="/runs">Back to runs</Link>
      </p>
    </section>
  );
}
