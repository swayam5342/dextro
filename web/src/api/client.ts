import createClient from "openapi-fetch";

import type { paths } from "./schema";

/** The one API client. The UI and API share an origin, so no base URL is needed. */
export const api = createClient<paths>({ credentials: "same-origin" });
