import { setupServer } from "msw/node";
import { handlers } from "./handlers";

/** One MSW server for the unit tests; started in setup.ts, unhandled requests fail the test. */
export const server = setupServer(...handlers);
