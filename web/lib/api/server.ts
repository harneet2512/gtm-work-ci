// Server-only entry: builds the core client from the environment. Import it from server components and
// route handlers only; the token it reads must never reach a client component.
import "server-only";
import { coreConfigFromEnv, createCoreClient } from "./core-client";

export const core = () => createCoreClient(coreConfigFromEnv(process.env));
