"use client";

import { UUID } from "@/lib/uuid";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";

/** The way into /control (or another manifest-scoped view): the core has no manifest list, so the
 * frozen manifest id is typed here. `target` lets /system reuse it for the leak-check read. */
export function ControlManifestForm({ initial = "", target = "/control", submitLabel = "Open control" }: { initial?: string; target?: string; submitLabel?: string }) {
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
    router.push(`${target}?manifest=${manifestId}`);
  };

  return (
    <form className="manifest-form" onSubmit={open}>
      <label htmlFor="control-manifest-id">Demo manifest id</label>
      <div className="row">
        <input
          id="control-manifest-id"
          name="manifest"
          value={id}
          onChange={(e) => setId(e.target.value)}
          placeholder="0d3a0000-0000-4000-8000-000000000501"
          spellCheck={false}
          autoComplete="off"
          required
        />
        <button type="submit">{submitLabel}</button>
      </div>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
    </form>
  );
}
