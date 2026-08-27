export interface Administrator {
  id: string;
  email: string;
  createdAt: string;
}

export interface SessionResponse {
  administrator: Administrator;
  csrfToken: string;
  expiresAt: string;
}

export interface SetupInput {
  setupToken: string;
  email: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

export type SessionState =
  | { kind: "loading" }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; administrator: Administrator; csrfToken: string };

export interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    details: Record<string, string[]>;
  };
}
