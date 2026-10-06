// The force layout (d3-force), ticked by the caller: synchronously before the first frame (so the graph
// never explodes on screen) and then one tick per animation frame while it settles. The graph is one connected
// structure: the account held at the centre, the deal beside it, and every other node pulled to the ring of its
// link distance from the account, while links, charge and collision arrange it. Pinned nodes do not move.
import { forceCollide, forceLink, forceManyBody, forceRadial, forceSimulation, type Simulation, type SimulationLinkDatum, type SimulationNodeDatum } from "d3-force";
import { ringRadius, seedPoint, type Point } from "./geometry";
import type { ExplorerModel, ExplorerNode } from "./model";

export const PRELAYOUT_TICKS = 300;

const LINK_DISTANCE = 90;
const LINK_STRENGTH = 0.08;
const CHARGE_PER_RADIUS = -32;
const CHARGE_RANGE = 460;
const COLLIDE_PAD = 16;
const RING_STRENGTH = 0.55;
const DRAG_HEAT = 0.25;
const VELOCITY_DECAY = 0.42;
/** How often (in ticks) the pre-layout looks at the clock. */
const BUDGET_CHECK = 5;

interface SimNode extends SimulationNodeDatum {
  id: string;
  node: ExplorerNode;
  ring: number;
}
export interface Layout {
  /**
   * Runs up to `ticks` ticks synchronously (no rendering), stopping early once `budgetMs` has passed on
   * `clock`, so a large graph never blocks the first frame for long; the rest settles live. Returns the
   * number of ticks run.
   */
  prelayout(ticks: number, budgetMs?: number, clock?: () => number): number;
  /** One live tick. False once the layout has cooled (nothing to animate). */
  tick(): boolean;
  /** Warms the layout up to at least `alpha` so it moves again. */
  reheat(alpha: number): void;
  /** Stops all motion at once (the next tick reports cooled). */
  cool(): void;
  /** The live point of a node (read-only; valid until the next tick). */
  point(id: string): Readonly<Point> | undefined;
  positions(): ReadonlyMap<string, Point>;
  /** Pins a node at a point (it does not move until freed). */
  hold(id: string, p: Point): void;
  /** Lets a held node move again. */
  free(id: string): void;
  /** Holds a node at a point while it is dragged, and warms the layout so its neighbors follow. */
  drag(id: string, p: Point): void;
  /** Leaves a dragged node where it was dropped. */
  drop(id: string): void;
  /** Lets go of the nodes pinned at creation (after an entrance has settled). */
  release(): void;
  stop(): void;
}

/** The account the graph is centred on: the Account node, else the best-connected node. */
function centreOf(model: ExplorerModel): string | null {
  const account = model.nodes.find((n) => n.type === "Account");
  if (account) return account.id;
  let best: string | null = null;
  for (const n of model.nodes) if (best === null || (model.neighbors.get(n.id)?.size ?? 0) > (model.neighbors.get(best)?.size ?? 0)) best = n.id;
  return best;
}

/** Links from the account to every node (breadth-first); a node with no path sits one ring past the farthest. */
export function connectionDepths(model: ExplorerModel): ReadonlyMap<string, number> {
  const depth = new Map<string, number>();
  const centre = centreOf(model);
  if (centre === null) return depth;
  depth.set(centre, 0);
  let frontier = [centre];
  while (frontier.length > 0) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const nb of model.neighbors.get(id) ?? []) {
        if (!depth.has(nb)) {
          depth.set(nb, depth.get(id)! + 1);
          next.push(nb);
        }
      }
    }
    frontier = next;
  }
  const beyond = Math.max(...depth.values()) + 1;
  for (const n of model.nodes) if (!depth.has(n.id)) depth.set(n.id, beyond);
  return depth;
}

/** Fresh seed points for a model: each node on its ring, at an angle from its id. */
export function seedFor(model: ExplorerModel): (node: ExplorerNode) => Point {
  const depth = connectionDepths(model);
  return (node) => seedPoint(node.id, depth.get(node.id) ?? 1, node.type);
}

export function createLayout(model: ExplorerModel, seed: ReadonlyMap<string, Point>, pinned: ReadonlySet<string>): Layout {
  const depth = connectionDepths(model);
  const centre = centreOf(model);
  const fresh = seedFor(model);
  const nodes: SimNode[] = model.nodes.map((node) => {
    const p = seed.get(node.id) ?? fresh(node);
    // The account is the anchor of the whole structure: held where it is (the centre on a fresh layout).
    const fixed = pinned.has(node.id) || node.id === centre ? { fx: p.x, fy: p.y } : {};
    return { id: node.id, node, ring: ringRadius(depth.get(node.id) ?? 1, node.type), x: p.x, y: p.y, ...fixed };
  });
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const links: SimulationLinkDatum<SimNode>[] = model.edges.map((e) => ({ source: e.source, target: e.target }));

  const sim: Simulation<SimNode, SimulationLinkDatum<SimNode>> = forceSimulation(nodes)
    .velocityDecay(VELOCITY_DECAY)
    .force("link", forceLink<SimNode, SimulationLinkDatum<SimNode>>(links).id((d) => d.id).distance(LINK_DISTANCE).strength(LINK_STRENGTH))
    .force("charge", forceManyBody<SimNode>().strength((d) => d.node.radius * CHARGE_PER_RADIUS).distanceMax(CHARGE_RANGE))
    .force("collide", forceCollide<SimNode>((d) => d.node.radius + COLLIDE_PAD).iterations(2))
    .force("ring", forceRadial<SimNode>((d) => d.ring, 0, 0).strength(RING_STRENGTH))
    .stop();

  const cooled = (): boolean => sim.alpha() < sim.alphaMin();

  function hold(id: string, p: Point): void {
    const n = byId.get(id);
    if (!n) return;
    n.fx = p.x;
    n.fy = p.y;
    n.x = p.x;
    n.y = p.y;
  }

  function free(id: string): void {
    const n = byId.get(id);
    if (!n) return;
    n.fx = null;
    n.fy = null;
  }

  return {
    prelayout(ticks, budgetMs = Infinity, clock = () => performance.now()) {
      const start = clock();
      let ran = 0;
      while (ran < ticks && !cooled()) {
        sim.tick();
        ran += 1;
        if (ran % BUDGET_CHECK === 0 && clock() - start >= budgetMs) break;
      }
      return ran;
    },
    tick() {
      if (cooled()) return false;
      sim.tick();
      return true;
    },
    reheat: (alpha) => void sim.alpha(Math.max(sim.alpha(), alpha)),
    cool: () => void sim.alpha(0),
    // The live simulation node itself (it has x and y): no allocation per frame.
    point: (id) => byId.get(id) as Readonly<Point> | undefined,
    positions: () => new Map(nodes.map((n) => [n.id, { x: n.x!, y: n.y! }])),
    hold,
    free,
    drag(id, p) {
      if (!byId.has(id)) return;
      hold(id, p);
      sim.alphaTarget(DRAG_HEAT);
      sim.alpha(Math.max(sim.alpha(), DRAG_HEAT));
    },
    drop(id) {
      if (byId.has(id)) sim.alphaTarget(0);
    },
    release() {
      for (const id of pinned) free(id);
    },
    stop: () => void sim.stop(),
  };
}
