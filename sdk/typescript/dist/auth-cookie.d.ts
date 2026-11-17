export interface AuthCookieOptions {
    key?: string;
    httpOnly?: boolean;
    secure?: boolean;
    sameSite?: "Lax" | "Strict" | "None";
    path?: string;
}
export declare function validateAuthCookieName(key: string): void;
export declare function serializeAuthCookie(state: unknown, options: AuthCookieOptions): string;
