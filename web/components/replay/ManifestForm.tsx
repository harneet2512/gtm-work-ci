"use client";

import { UUID } from "@/lib/uuid";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";


/**
 * The only way into a replay: the core has no manifest list endpoint, so a manifest id (uuid, printed
 * by `ghostctl freeze` when the demo is frozen) is typed here and stored in the URL as /replay/{id}.
 */
export function ManifestForm({ initial = "" }: { initial?: string }) {
  const router = useRouter();
  const [id, setId] = useState(initial);
  const [error, setError] = useState<string | null>(null);

  const open = (e: FormEvent) => {
    e.preventDefault();
    const manifestId = id.trim().toLowerCase();
    if (!UUID.test(manifestId)) {
      setError("A manifest id is a uuid, for example 0d3a0000-0000-4000-8000-000000000501.");
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
