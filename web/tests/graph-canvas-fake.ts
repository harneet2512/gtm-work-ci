// A recording stand-in for CanvasRenderingContext2D (jsdom has no canvas): every method call is logged,
// properties keep what was assigned, and text measures 6px per character.
export interface Recorded {
  name: string;
  args: unknown[];
  /** The fill style and font in effect when the call was made. */
  fillStyle: unknown;
  font: unknown;
}

export interface FakeContext {
  ctx: CanvasRenderingContext2D;
  calls: Recorded[];
  /** Every string drawn with fillText, in order. */
  texts: () => string[];
  reset: () => void;
}

const DEFAULTS: Record<string, unknown> = {
  globalAlpha: 1,
  lineWidth: 1,
  fillStyle: "#000",
  strokeStyle: "#000",
  font: "10px sans-serif",
  textAlign: "start",
  textBaseline: "alphabetic",
  lineDashOffset: 0,
};

export function fakeContext(): FakeContext {
  const calls: Recorded[] = [];
  const state: Record<string, unknown> = { ...DEFAULTS };
  const ctx = new Proxy(
    {},
    {
      get(_t, prop: string) {
        if (prop === "measureText") return (text: string) => ({ width: text.length * 6 });
        if (prop in state) return state[prop];
        return (...args: unknown[]) => void calls.push({ name: prop, args, fillStyle: state.fillStyle, font: state.font });
      },
      set(_t, prop: string, value: unknown) {
        state[prop] = value;
        return true;
      },
    },
  ) as unknown as CanvasRenderingContext2D;
  return {
    ctx,
    calls,
    texts: () => calls.filter((c) => c.name === "fillText").map((c) => String(c.args[0])),
    reset: () => void calls.splice(0, calls.length),
  };
}
