// Hand-written mirror of api/openapi.yaml, in the shape openapi-fetch expects.
// Nothing generates or checks this file: when the contract changes, update it
// in the same commit.

export interface components {
  schemas: {
    Health: {
      status: "ok" | "unavailable";
    };
    AuthConfig: {
      /** Whether a new account can be created right now. */
      registration_open: boolean;
      google_enabled: boolean;
    };
    Credentials: {
      email: string;
      password: string;
    };
    ChangePassword: {
      current_password: string;
      new_password: string;
    };
    User: {
      id: string;
      email: string;
      role: "user" | "admin";
      /** False for accounts created through Google. */
      has_password: boolean;
      google_linked: boolean;
      created_at: string;
    };
    Session: {
      id: string;
      /** True for the session making this request. */
      current: boolean;
      user_agent: string;
      ip: string;
      created_at: string;
      last_seen_at: string;
      expires_at: string;
    };
    GoogleRedirect: {
      url: string;
    };
    Connection: {
      id: string;
      name: string;
      provider: "s3" | "google_drive";
      /** What the connection points at, for showing to its owner. */
      summary: string;
      created_at: string;
    };
    S3ConnectionInput: {
      name: string;
      /** The service's base URL, such as https://s3.example.com. */
      endpoint: string;
      /** Defaults to us-east-1. */
      region?: string;
      bucket: string;
      access_key_id: string;
      secret_access_key: string;
    };
    Settings: {
      /**
       * When false, nobody can create an account, by password or through a
       * first-time Google sign-in. The first account on an empty instance can
       * always be created.
       */
      allow_registration: boolean;
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

type Schemas = components["schemas"];

type JsonBody<T> = {
  headers: { [name: string]: unknown };
  content: { "application/json": T };
};

type NoContent = {
  headers: { [name: string]: unknown };
  content?: never;
};

type NoParameters = {
  query?: never;
  header?: never;
  path?: never;
  cookie?: never;
};

type JsonRequest<T> = {
  content: { "application/json": T };
};

/** An operation with no parameters and no request body. */
type Operation<Responses> = {
  parameters: NoParameters;
  requestBody?: never;
  responses: Responses & { default: JsonBody<Schemas["Error"]> };
};

/** An operation that takes a JSON body. */
type JsonOperation<Body, Responses> = {
  parameters: NoParameters;
  requestBody: JsonRequest<Body>;
  responses: Responses & { default: JsonBody<Schemas["Error"]> };
};

/** An operation on one item, identified by its ID in the path. */
type PathOperation = {
  parameters: {
    query?: never;
    header?: never;
    path: { id: string };
    cookie?: never;
  };
  requestBody?: never;
  responses: {
    204: NoContent;
    default: JsonBody<Schemas["Error"]>;
  };
};

export interface paths {
  "/health": {
    get: {
      parameters: NoParameters;
      requestBody?: never;
      responses: {
        200: JsonBody<Schemas["Health"]>;
        503: JsonBody<Schemas["Health"]>;
      };
    };
  };
  "/api/v1/auth/config": {
    get: Operation<{ 200: JsonBody<Schemas["AuthConfig"]> }>;
  };
  "/api/v1/auth/register": {
    post: JsonOperation<Schemas["Credentials"], { 201: JsonBody<Schemas["User"]> }>;
  };
  "/api/v1/auth/login": {
    post: JsonOperation<Schemas["Credentials"], { 200: JsonBody<Schemas["User"]> }>;
  };
  "/api/v1/auth/logout": {
    post: Operation<{ 204: NoContent }>;
  };
  "/api/v1/auth/me": {
    get: Operation<{ 200: JsonBody<Schemas["User"]> }>;
  };
  "/api/v1/auth/password": {
    post: JsonOperation<Schemas["ChangePassword"], { 204: NoContent }>;
  };
  "/api/v1/auth/sessions": {
    get: Operation<{ 200: JsonBody<Schemas["Session"][]> }>;
  };
  "/api/v1/auth/sessions/{id}": {
    delete: PathOperation;
  };
  "/api/v1/auth/google/link": {
    post: Operation<{ 200: JsonBody<Schemas["GoogleRedirect"]> }>;
  };
  "/api/v1/storage/connections": {
    get: Operation<{ 200: JsonBody<Schemas["Connection"][]> }>;
  };
  "/api/v1/storage/connections/s3": {
    post: JsonOperation<Schemas["S3ConnectionInput"], { 201: JsonBody<Schemas["Connection"]> }>;
  };
  "/api/v1/storage/connections/{id}": {
    delete: PathOperation;
  };
  "/api/v1/storage/connections/{id}/test": {
    post: PathOperation;
  };
  "/api/v1/storage/google/connect": {
    post: Operation<{ 200: JsonBody<Schemas["GoogleRedirect"]> }>;
  };
  "/api/v1/admin/settings": {
    get: Operation<{ 200: JsonBody<Schemas["Settings"]> }>;
    patch: JsonOperation<Schemas["Settings"], { 200: JsonBody<Schemas["Settings"]> }>;
  };
}
