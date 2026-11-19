export class PgBaseError extends Error {
    status;
    code;
    details;
    etag;
    retryAfter;
    constructor(status, code, details = {}, etag, retryAfter) {
        super(code);
        this.status = status;
        this.code = code;
        this.details = details;
        this.etag = etag;
        this.retryAfter = retryAfter;
        this.name = "PgBaseError";
    }
}
export class MFAChallengeError extends PgBaseError {
    challenge;
    constructor(status, code, details, challenge) {
        super(status, code, details);
        this.challenge = challenge;
        this.name = "MFAChallengeError";
    }
}
