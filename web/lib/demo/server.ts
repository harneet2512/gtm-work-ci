// Server-only entry for the demo control service: reads the environment (the token has no NEXT_PUBLIC_ prefix), so it
// is imported from server components, route handlers and server actions only.
import { controlConfigFromEnv, type ControlConfig } from "./control-client";

export const demoControl = (): ControlConfig => controlConfigFromEnv(process.env);
