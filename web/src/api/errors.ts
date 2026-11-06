import type { components } from "./schema";

/** A failed API call, carrying the stable code and the message that's safe to show. */
export class ApiError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.code = code;
  }
}

function isErrorBody(value: unknown): value is components["schemas"]["Error"] {
  return typeof value === "object" && value !== null && "error" in value;
}

/** Turns the error from an openapi-fetch call into an ApiError. */
export function toApiError(error: unknown): ApiError {
  if (isErrorBody(error)) {
    return new ApiError(error.error.code, error.error.message);
  }
  return new ApiError("unknown", "The server sent an unexpected response. Try again.");
}

/** The message to show for something caught, which may not have come from the API. */
export function failureMessage(error: unknown): string {
  return (error instanceof ApiError ? error : toApiError(error)).message;
}
