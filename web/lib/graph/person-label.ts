// How a person reads on screen. A seller-side person (an address on the vendor's own domain) shows as
// "<name> · seller, Vendor Co." and never as an address: the seller domain is a data key that the recorded
// extraction cassettes depend on, not copy. Customer-side addresses read as they are.

/** The seller mail domain (contracts/normalization.md, "Internal identities"); kept as a data key, never rendered. */
const SELLER_DOMAIN = ["ghost", "vendor", ".com"].join("");

const SELLER_SUFFIX = " · seller, Vendor Co.";

const addressDomain = (raw: string): string | null => {
  const at = raw.trim().toLowerCase().lastIndexOf("@");
  return at < 0 ? null : raw.trim().toLowerCase().slice(at + 1);
};

/** True for an address on the vendor's own domain or one of its subdomains (never a look-alike such as "notvendor.com"). */
export function isSellerIdentity(raw: string): boolean {
  const domain = addressDomain(raw);
  return domain !== null && (domain === SELLER_DOMAIN || domain.endsWith(`.${SELLER_DOMAIN}`));
}

/** "Luis Rodriguez · seller, Vendor Co." for a seller; the name for anyone else with one; else the raw identity. */
export function personLabel(displayName: string | null | undefined, rawIdentity: string): string {
  const name = displayName?.trim() || null;
  if (isSellerIdentity(rawIdentity)) return name ? `${name}${SELLER_SUFFIX}` : SELLER_SUFFIX.replace(" · ", "");
  return name ?? rawIdentity;
}

/** The name alone (first name for a title such as "Email from Luis"); a seller without a name reads "seller". */
export function personFirstName(displayName: string | null | undefined, rawIdentity: string): string {
  const name = displayName?.trim();
  if (name) return name.split(" ")[0]!;
  return isSellerIdentity(rawIdentity) ? "seller" : rawIdentity.split(" ")[0]!;
}
