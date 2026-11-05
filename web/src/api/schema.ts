// Hand-written mirror of api/openapi.yaml, in the shape openapi-fetch expects.
// Nothing generates or checks this file: when the contract changes, update it
// in the same commit.

export interface components {
  schemas: {
    Health: {
      status: "ok" | "unavailable";
    };
    AuthConfig: {
      /** True only on an empty server, where the first account becomes the admin. */
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
      /** The admin chose this password, so nothing else works until it's replaced. */
      must_change_password: boolean;
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
      provider: "google_drive";
      /** What the connection points at, for showing to its owner. */
      summary: string;
      created_at: string;
    };
    PoolAccount: {
      connection: components["schemas"]["Connection"];
      /** False if the account can't be reached, usually because its token expired. */
      available: boolean;
      used_bytes: number;
      /** Null if the account has no limit, or can't be reached. */
      limit_bytes: number | null;
    };
    Pool: {
      accounts: components["schemas"]["PoolAccount"][];
      used_bytes: number;
      /** Null if any working account has no limit. */
      limit_bytes: number | null;
      /** Null if any working account has no limit. */
      free_bytes: number | null;
    };
    /** An account as the admin sees it. It says nothing about the person's files. */
    ManagedUser: {
      id: string;
      email: string;
      role: "user" | "admin";
      has_password: boolean;
      google_linked: boolean;
      must_change_password: boolean;
      /** How many storage accounts this person has linked. */
      linked_accounts: number;
      created_at: string;
    };
    NewPassword: {
      password: string;
    };
    SearchResults: {
      folders: components["schemas"]["Folder"][];
      files: components["schemas"]["File"][];
    };
    Settings: {
      /** The largest file anyone can upload. */
      max_upload_bytes: number;
    };
    /** Only the fields present are changed. */
    SettingsUpdate: {
      max_upload_bytes?: number;
    };
    File: {
      id: string;
      name: string;
      size: number;
      content_type: string;
      /** How the file can be shown inline, if at all. */
      preview: "image" | "video" | "pdf" | "none";
      connection_id: string;
      folder_id: string | null;
      trashed_at: string | null;
      created_at: string;
      updated_at: string;
    };
    Folder: {
      id: string;
      name: string;
      parent_id: string | null;
      trashed_at: string | null;
      created_at: string;
      updated_at: string;
    };
    FolderContents: {
      /** The folders from the top level down to this one. */
      path: components["schemas"]["Folder"][];
      folders: components["schemas"]["Folder"][];
      files: components["schemas"]["File"][];
    };
    Trash: {
      folders: components["schemas"]["Folder"][];
      files: components["schemas"]["File"][];
    };
    NameInput: {
      name: string;
    };
    FolderInput: {
      name: string;
      parent_id: string | null;
    };
    MoveFileInput: {
      /** Null moves it to the top level. */
      folder_id: string | null;
    };
    MoveFolderInput: {
      /** Null moves it to the top level. */
      parent_id: string | null;
    };
    APIKey: {
      id: string;
      name: string;
      /** The start of the key, for telling keys apart. */
      prefix: string;
      scopes: ("files:read" | "files:write")[];
      created_at: string;
      last_used_at: string | null;
    };
    APIKeyInput: {
      name: string;
      scopes: ("files:read" | "files:write")[];
    };
    APIKeyCreated: {
      key: components["schemas"]["APIKey"];
      /** The secret, sent as a bearer token. Shown only this once. */
      token: string;
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

/** An operation on one item that also takes a JSON body. */
type IdJsonOperation<Body, Responses> = {
  parameters: {
    query?: never;
    header?: never;
    path: { id: string };
    cookie?: never;
  };
  requestBody: JsonRequest<Body>;
  responses: Responses & { default: JsonBody<Schemas["Error"]> };
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
  "/api/v1/auth/api-keys": {
    get: Operation<{ 200: JsonBody<Schemas["APIKey"][]> }>;
    post: JsonOperation<Schemas["APIKeyInput"], { 201: JsonBody<Schemas["APIKeyCreated"]> }>;
  };
  "/api/v1/auth/api-keys/{id}": {
    delete: PathOperation;
  };
  "/api/v1/auth/google/link": {
    post: Operation<{ 200: JsonBody<Schemas["GoogleRedirect"]> }>;
  };
  "/api/v1/storage/connections": {
    get: Operation<{ 200: JsonBody<Schemas["Connection"][]> }>;
  };
  "/api/v1/storage/connections/{id}": {
    delete: PathOperation;
  };
  "/api/v1/storage/connections/{id}/test": {
    post: PathOperation;
  };
  "/api/v1/storage/pool": {
    get: Operation<{ 200: JsonBody<Schemas["Pool"]> }>;
  };
  "/api/v1/storage/google/connect": {
    post: Operation<{ 200: JsonBody<Schemas["GoogleRedirect"]> }>;
  };
  "/api/v1/files": {
    get: {
      parameters: {
        query?: { folder_id?: string };
        header?: never;
        path?: never;
        cookie?: never;
      };
      requestBody?: never;
      responses: {
        200: JsonBody<Schemas["FolderContents"]>;
        default: JsonBody<Schemas["Error"]>;
      };
    };
  };
  "/api/v1/files/{id}": {
    patch: IdJsonOperation<Schemas["NameInput"], { 200: JsonBody<Schemas["File"]> }>;
    delete: PathOperation;
  };
  "/api/v1/files/{id}/thumbnail": {
    get: {
      parameters: {
        query?: never;
        header?: never;
        path: { id: string };
        cookie?: never;
      };
      requestBody?: never;
      responses: {
        200: { headers: { [name: string]: unknown }; content: { "image/*": string } };
        default: JsonBody<Schemas["Error"]>;
      };
    };
  };
  "/api/v1/files/{id}/move": {
    post: IdJsonOperation<Schemas["MoveFileInput"], { 200: JsonBody<Schemas["File"]> }>;
  };
  "/api/v1/files/{id}/trash": {
    post: PathOperation;
  };
  "/api/v1/files/{id}/restore": {
    post: PathOperation;
  };
  "/api/v1/folders": {
    post: JsonOperation<Schemas["FolderInput"], { 201: JsonBody<Schemas["Folder"]> }>;
  };
  "/api/v1/folders/{id}": {
    patch: IdJsonOperation<Schemas["NameInput"], { 200: JsonBody<Schemas["Folder"]> }>;
    delete: PathOperation;
  };
  "/api/v1/folders/{id}/move": {
    post: IdJsonOperation<Schemas["MoveFolderInput"], { 200: JsonBody<Schemas["Folder"]> }>;
  };
  "/api/v1/folders/{id}/trash": {
    post: PathOperation;
  };
  "/api/v1/folders/{id}/restore": {
    post: PathOperation;
  };
  "/api/v1/search": {
    get: {
      parameters: {
        query: { q: string };
        header?: never;
        path?: never;
        cookie?: never;
      };
      requestBody?: never;
      responses: {
        200: JsonBody<Schemas["SearchResults"]>;
        default: JsonBody<Schemas["Error"]>;
      };
    };
  };
  "/api/v1/trash": {
    get: Operation<{ 200: JsonBody<Schemas["Trash"]> }>;
    delete: Operation<{ 204: NoContent }>;
  };
  "/api/v1/admin/settings": {
    get: Operation<{ 200: JsonBody<Schemas["Settings"]> }>;
    patch: JsonOperation<Schemas["SettingsUpdate"], { 200: JsonBody<Schemas["Settings"]> }>;
  };
  "/api/v1/admin/users": {
    get: Operation<{ 200: JsonBody<Schemas["ManagedUser"][]> }>;
    post: JsonOperation<Schemas["Credentials"], { 201: JsonBody<Schemas["ManagedUser"]> }>;
  };
  "/api/v1/admin/users/{id}": {
    delete: PathOperation;
  };
  "/api/v1/admin/users/{id}/password": {
    post: IdJsonOperation<Schemas["NewPassword"], { 204: NoContent }>;
  };
}
