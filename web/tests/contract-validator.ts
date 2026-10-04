// Ajv 2020-12 over every contracts/schemas/*.json plus the components.schemas of contracts/openapi/core.yaml,
// the TypeScript twin of the Go and Python conformance tests. Fixtures the web renders must pass here.
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020, { type ErrorObject } from "ajv/dist/2020";
import addFormats from "ajv-formats";
import { parse } from "yaml";

const here = path.dirname(fileURLToPath(import.meta.url));
export const CONTRACTS_DIR = path.resolve(here, "../../contracts");
export const FIXTURES_DIR = path.join(here, "fixtures", "core");
const SCHEMAS_DIR = path.join(CONTRACTS_DIR, "schemas");
const SCHEMA_SUFFIX = ".v1.json";
const BASE = "https://ghost.local/contracts/";
const OPENAPI_ID = `${BASE}openapi-core`;

export function readJson<T = unknown>(file: string): T {
  return JSON.parse(readFileSync(file, "utf8")) as T;
}

export const loadExample = <T = unknown>(name: string): T => readJson<T>(path.join(CONTRACTS_DIR, "examples", `${name}.example.json`));
export const loadFixture = <T = unknown>(name: string): T => readJson<T>(path.join(FIXTURES_DIR, name));

/** Rewrites the OpenAPI refs into ids Ajv knows: ../schemas/x.v1.json#... and #/components/schemas/X. */
function rewriteRefs(node: unknown): unknown {
  if (Array.isArray(node)) return node.map(rewriteRefs);
  if (node === null || typeof node !== "object") return node;
  return Object.fromEntries(
    Object.entries(node).map(([key, value]) => {
      if (key === "$ref" && typeof value === "string") {
        return [key, value.replace("../schemas/", BASE).replace("#/components/schemas/", `${OPENAPI_ID}#/$defs/`)];
      }
      return [key, rewriteRefs(value)];
    }),
  );
}

function createAjv(): Ajv2020 {
  const ajv = new Ajv2020({ allErrors: true, strict: false, allowUnionTypes: true });
  addFormats(ajv);
  for (const file of readdirSync(SCHEMAS_DIR).filter((f) => f.endsWith(SCHEMA_SUFFIX))) {
    ajv.addSchema(readJson<object>(path.join(SCHEMAS_DIR, file)));
  }
  const core = parse(readFileSync(path.join(CONTRACTS_DIR, "openapi/core.yaml"), "utf8")) as {
    components: { schemas: Record<string, unknown> };
  };
  ajv.addSchema({ $id: OPENAPI_ID, $defs: rewriteRefs(core.components.schemas) as Record<string, unknown> });
  return ajv;
}

const ajv = createAjv();

export interface ValidationResult {
  valid: boolean;
  errors: string[];
}

function run(ref: string, data: unknown): ValidationResult {
  const fn = ajv.getSchema(ref);
  if (!fn) throw new Error(`schema not loaded: ${ref}`);
  const valid = fn(data) as boolean;
  const errors = (fn.errors ?? []).map((e: ErrorObject) => `${e.instancePath || "/"} ${e.message ?? e.keyword}`);
  return { valid, errors };
}

/** Validates against contracts/schemas/<name>.v1.json. */
export const validateSchema = (name: string, data: unknown) => run(`${BASE}${name}${SCHEMA_SUFFIX}`, data);

/** Validates against a components.schemas entry of contracts/openapi/core.yaml (Graph, GraphDiff, ...). */
export const validateComponent = (name: string, data: unknown) => run(`${OPENAPI_ID}#/$defs/${name}`, data);
