export class PgBaseError extends Error {
    status;
    code;
    details;
    etag;
    constructor(status, code, details = {}, etag) {
        super(code);
        this.status = status;
        this.code = code;
        this.details = details;
        this.etag = etag;
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
