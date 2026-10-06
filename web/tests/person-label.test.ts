import { describe, expect, it } from "vitest";
import { activityTitle } from "@/lib/graph/labels";
import { isSellerIdentity, personFirstName, personLabel } from "@/lib/graph/person-label";
import type { Activity } from "@/lib/api/types";

const SELLER = ["luis.rodriguez", "@", "ghost", "vendor", ".com"].join("");

describe("person labels", () => {
  it("shows a seller as name, seller, Vendor Co. with no address", () => {
    const label = personLabel("Luis Rodriguez", SELLER);
    expect(label).toBe("Luis Rodriguez · seller, Vendor Co.");
    expect(label).not.toMatch(/@|ghostvendor/i);
  });

  it("shows a nameless seller without the address", () => {
    expect(personLabel(null, SELLER)).toBe("seller, Vendor Co.");
    expect(personLabel("  ", SELLER)).toBe("seller, Vendor Co.");
  });

  it("leaves customer-side people as they are", () => {
    expect(personLabel("Fatoumata Toure", "fatoumata.toure@medtechadvances.com")).toBe("Fatoumata Toure");
    expect(personLabel(null, "fatoumata.toure@medtechadvances.com")).toBe("fatoumata.toure@medtechadvances.com");
  });

  it("recognises subdomains and case, not look-alikes", () => {
    expect(isSellerIdentity(SELLER.toUpperCase())).toBe(true);
    expect(isSellerIdentity(SELLER.replace("@", "@mail."))).toBe(true);
    expect(isSellerIdentity(SELLER.replace("@", "@not"))).toBe(false);
    expect(isSellerIdentity(`${SELLER}.evil.io`)).toBe(false);
    expect(isSellerIdentity("crm:contact:817")).toBe(false);
    expect(isSellerIdentity("")).toBe(false);
  });

  it("uses the first name in a title and never the seller address", () => {
    expect(personFirstName("Luis Rodriguez", SELLER)).toBe("Luis");
    expect(personFirstName(null, SELLER)).toBe("seller");
  });

  it("keeps the seller address out of activity titles", () => {
    const activity = { participants: [{ raw_identity: SELLER, display_name: null, role: "from", person_id: null }] } as unknown as Activity;
    expect(activityTitle("EmailSent", activity)).not.toMatch(/@|ghostvendor/i);
    const added = { participants: [{ raw_identity: SELLER, display_name: null, role: "mentioned", person_id: null }] } as unknown as Activity;
    expect(activityTitle("ContactAdded", added)).not.toMatch(/@|ghostvendor/i);
  });
});
