// Types for fixture-disputes.mjs (the fixture core's POST /eval-results/{id}/disputes).
export interface DisputeReply {
  status: number;
  // The EvalDispute on 200/201, the error envelope otherwise.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  body: any;
}

export function createDisputeStore(strategies: unknown, trace: unknown): { dispute(resultId: string, body: unknown): DisputeReply };
