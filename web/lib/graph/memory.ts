// Layout memory: where every node of an account was and where the camera looked, so Before Play -> After
// Play applies the diff on top of the same picture instead of laying the graph out again. Held in memory
// for client navigation and mirrored to sessionStorage for a reload. Stored data is untrusted on read.
import type { Camera } from "./camera";
import type { Point } from "./geometry";

export interface MemoryStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export interface Remembered {
  positions: ReadonlyMap<string, Point>;
  /** Where the camera looked: the world point at the viewport center (x, y) and the zoom (k). */
  camera: Camera;
  /** What each node was called, so something the next view no longer holds can still be named. */
  labels: ReadonlyMap<string, string>;
}

export interface LayoutMemory {
  recall(accountId: string): Remembered | null;
  remember(accountId: string, positions: ReadonlyMap<string, Point>, camera: Camera, labels?: ReadonlyMap<string, string>): void;
}

const PREFIX = "ghost.graph.v1:";
const MAX_ENTRIES = 2000;
const DEFAULT_CAMERA: Camera = { x: 0, y: 0, k: 1 };

const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

function parsePoint(v: unknown): Point | null {
  if (typeof v !== "object" || v === null) return null;
  const { x, y } = v as Record<string, unknown>;
  return finite(x) && finite(y) ? { x, y } : null;
}

function parseCamera(v: unknown): Camera {
  if (typeof v !== "object" || v === null) return DEFAULT_CAMERA;
  const { x, y, k } = v as Record<string, unknown>;
  return finite(x) && finite(y) && finite(k) && k > 0 ? { x, y, k } : DEFAULT_CAMERA;
}

function parseLabels(v: unknown): Map<string, string> {
  const out = new Map<string, string>();
  if (!Array.isArray(v)) return out;
  for (const entry of v.slice(0, MAX_ENTRIES)) {
    if (Array.isArray(entry) && typeof entry[0] === "string" && typeof entry[1] === "string") out.set(entry[0], entry[1]);
  }
  return out;
}

function parse(raw: string): Remembered | null {
  try {
    const data = JSON.parse(raw) as { positions?: unknown; camera?: unknown; labels?: unknown };
    if (!Array.isArray(data.positions)) return null;
    const positions = new Map<string, Point>();
    for (const entry of data.positions.slice(0, MAX_ENTRIES)) {
      if (!Array.isArray(entry) || typeof entry[0] !== "string") continue;
      const p = parsePoint(entry[1]);
      if (p) positions.set(entry[0], p);
    }
    return { positions, camera: parseCamera(data.camera), labels: parseLabels(data.labels) };
  } catch {
    return null;
  }
}

function read(storage: MemoryStorage | null, accountId: string): Remembered | null {
  try {
    const raw = storage?.getItem(PREFIX + accountId);
    return raw ? parse(raw) : null;
  } catch {
    return null;
  }
}

function write(storage: MemoryStorage | null, accountId: string, value: Remembered): void {
  try {
    storage?.setItem(PREFIX + accountId, JSON.stringify({ positions: [...value.positions], camera: value.camera, labels: [...value.labels] }));
  } catch {
    // Storage full or denied (private mode): the in-memory copy still serves client navigation.
  }
}

export function createLayoutMemory(storage: MemoryStorage | null): LayoutMemory {
  const cache = new Map<string, Remembered>();
  return {
    recall(accountId) {
      const hit = cache.get(accountId) ?? read(storage, accountId);
      if (hit) cache.set(accountId, hit);
      return hit;
    },
    remember(accountId, positions, camera, labels = new Map()) {
      const prior = cache.get(accountId) ?? read(storage, accountId);
      const merged: Remembered = {
        positions: new Map([...(prior?.positions ?? []), ...positions]),
        camera: { ...camera },
        labels: new Map([...(prior?.labels ?? []), ...labels]),
      };
      cache.set(accountId, merged);
      write(storage, accountId, merged);
    },
  };
}

function sessionStore(): MemoryStorage | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

let shared: LayoutMemory | null = null;

/** The browser's one layout memory (sessionStorage-backed when available). */
export function layoutMemory(): LayoutMemory {
  shared ??= createLayoutMemory(sessionStore());
  return shared;
}
