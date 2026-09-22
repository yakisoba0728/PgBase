/** Read errors remain transport errors; only invalid JSON uses the supplied fallback. */
export declare function responseJSON(response: Response, invalid: () => unknown): Promise<unknown>;
