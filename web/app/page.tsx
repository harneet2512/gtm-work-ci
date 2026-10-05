import Link from "next/link";
import { core } from "@/lib/api/server";

export const dynamic = "force-dynamic";

export default async function AccountsPage() {
  const accounts = await core().listAccounts();
  return (
    <section className="page">
      <h1>Accounts</h1>
      {accounts.length === 0 ? (
        <p className="empty">The core has no accounts yet.</p>
      ) : (
        <ul className="account-list">
          {accounts.map((a) => (
            <li key={a.id}>
              <Link href={`/accounts/${a.id}`}>{a.name}</Link>
              <span className="hint">{[a.stage, a.health, a.motion].filter(Boolean).join(" · ")}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
