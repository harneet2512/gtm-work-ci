"use client";

import { UUID } from "@/lib/uuid";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";

/** The fallback way into /control (or another manifest-scoped view) when no demo account is configured: the core has no
 * manifest list, so the id is entered here. With a configured default the pages open on it and this form is not shown.
 * `target` lets /system reuse it for the leak-check read. */
export function ControlManifestForm({ initial = "", target = "/control", submitLabel = "Open control" }: { initial?: string; target?: string; submitLabel?: string }) {
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
          placeholder="00000000-0000-4000-8000-000000000000"
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
