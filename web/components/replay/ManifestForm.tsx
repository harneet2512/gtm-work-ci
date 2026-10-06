"use client";

import { UUID } from "@/lib/uuid";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";


/**
 * The way into a replay when no demo account is configured: the core has no manifest list endpoint, so the id is
 * entered here and stored in the URL as /replay/{id}. With a configured default the index prefills it.
 */
export function ManifestForm({ initial = "" }: { initial?: string }) {
  const router = useRouter();
  const [id, setId] = useState(initial);
  const [error, setError] = useState<string | null>(null);

  const open = (e: FormEvent) => {
    e.preventDefault();
    const manifestId = id.trim().toLowerCase();
    if (!UUID.test(manifestId)) {
      setError("A manifest id is a uuid (letters a-f, digits, hyphens).");
      return;
    }
    setError(null);
    router.push(`/replay/${manifestId}`);
  };

  return (
    <form className="manifest-form" onSubmit={open}>
      <label htmlFor="manifest-id">Demo manifest id</label>
      <input
        id="manifest-id"
        name="manifest"
        value={id}
        onChange={(e) => setId(e.target.value)}
        placeholder="00000000-0000-4000-8000-000000000000"
        spellCheck={false}
        autoComplete="off"
      />
      <button type="submit">Open replay</button>
      {error ? (
        <p className="status-line err" role="alert">
          {error}
        </p>
      ) : null}
    </form>
  );
}
