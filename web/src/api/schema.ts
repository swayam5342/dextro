// Hand-written mirror of api/openapi.yaml, in the shape openapi-fetch expects.
// Nothing generates or checks this file: when the contract changes, update it
// in the same commit.

export interface components {
  schemas: {
    Health: {
      status: "ok" | "unavailable";
    };
    Error: {
      error: {
        /** Stable, machine-readable error code. */
        code: string;
        /** Safe, human-readable message. */
        message: string;
        /** Matches the X-Request-ID response header and the server logs. */
        request_id: string;
      };
    };
  };
}

type JsonBody<T> = {
  headers: { [name: string]: unknown };
  content: { "application/json": T };
};

export interface paths {
  "/health": {
    get: {
      parameters: {
        query?: never;
        header?: never;
        path?: never;
        cookie?: never;
      };
      requestBody?: never;
      responses: {
        200: JsonBody<components["schemas"]["Health"]>;
        503: JsonBody<components["schemas"]["Health"]>;
      };
    };
  };
}
