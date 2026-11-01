import {
    FetchLike,
    OAuthClientCredentialsAuthOptions,
    OAuthClientCredentialsProfile,
} from './types';

const DEFAULT_REFRESH_SKEW_MS = 30 * 1000;

interface CachedAccessToken {
    token: string;
    expiresAtMs: number;
}

interface ValidatedOAuthClientCredentialsProfile {
    tokenUrl: string;
    clientId: string;
    clientSecret: string;
    scopes: string[];
}

export class OAuthClientCredentialsAuthError extends Error {
    public readonly status?: number;

    constructor(message: string, status?: number) {
        super(message);
        this.name = 'OAuthClientCredentialsAuthError';
        this.status = status;
    }
}

/**
 * Acquires and caches short-lived OAuth access tokens using the standard
 * client_credentials grant. Client secrets and access tokens are never
 * included in errors.
 */
export class OAuthClientCredentialsAuthProvider {
    private readonly profile: ValidatedOAuthClientCredentialsProfile;
    private readonly fetchImpl: FetchLike;
    private readonly clock: () => number;
    private readonly refreshSkewMs: number;
    private cached?: CachedAccessToken;
    private refreshPromise?: Promise<string>;

    constructor(options: OAuthClientCredentialsAuthOptions, fallbackFetch?: FetchLike) {
        if (!options || options.type !== 'oauth-client-credentials') {
            throw new OAuthClientCredentialsAuthError('Invalid OAuth client-credentials authentication configuration');
        }
        this.profile = validateOAuthClientCredentialsProfile(options.profile);
        this.fetchImpl = options.fetch || fallbackFetch || defaultFetch();
        this.clock = options.clock || Date.now;
        this.refreshSkewMs = validateDuration(
            'refreshSkewMs',
            options.refreshSkewMs ?? DEFAULT_REFRESH_SKEW_MS,
            0,
            Number.MAX_SAFE_INTEGER,
        );
    }

    public async getToken(): Promise<string> {
        const now = this.now();
        if (this.cached && now < this.cached.expiresAtMs - this.refreshSkewMs) {
            return this.cached.token;
        }
        if (!this.refreshPromise) {
            this.refreshPromise = this.exchangeToken().finally(() => {
                this.refreshPromise = undefined;
            });
        }
        return this.refreshPromise;
    }

    private async exchangeToken(): Promise<string> {
        const body = new URLSearchParams({
            grant_type: 'client_credentials',
            client_id: this.profile.clientId,
            client_secret: this.profile.clientSecret,
        });
        if (this.profile.scopes.length > 0) {
            body.set('scope', this.profile.scopes.join(' '));
        }

        let response;
        try {
            response = await this.fetchImpl(this.profile.tokenUrl, {
                method: 'POST',
                headers: {
                    Accept: 'application/json',
                    'Content-Type': 'application/x-www-form-urlencoded',
                },
                body: body.toString(),
            });
        } catch {
            throw new OAuthClientCredentialsAuthError('OAuth client-credentials token exchange failed');
        }

        if (!response.ok) {
            throw new OAuthClientCredentialsAuthError(
                'OAuth client-credentials token exchange was rejected',
                response.status,
            );
        }

        let payload: unknown;
        try {
            payload = JSON.parse(await response.text());
        } catch {
            throw new OAuthClientCredentialsAuthError(
                'OAuth client-credentials token exchange returned an invalid response',
            );
        }

        const token = parseOAuthTokenResponse(payload);
        const now = this.now();
        this.cached = {
            token: token.accessToken,
            expiresAtMs: now + token.expiresInSeconds * 1000,
        };
        return token.accessToken;
    }

    private now(): number {
        let value: number;
        try {
            value = this.clock();
        } catch {
            throw new OAuthClientCredentialsAuthError('Authentication clock failed');
        }
        if (!Number.isFinite(value) || value < 0) {
            throw new OAuthClientCredentialsAuthError('Authentication clock returned an invalid value');
        }
        return value;
    }
}

export function createOAuthClientCredentialsAuthProvider(
    options: OAuthClientCredentialsAuthOptions,
): OAuthClientCredentialsAuthProvider {
    return new OAuthClientCredentialsAuthProvider(options);
}

function validateOAuthClientCredentialsProfile(
    profile: OAuthClientCredentialsProfile,
): ValidatedOAuthClientCredentialsProfile {
    if (!profile || profile.type !== 'oauth-client-credentials') {
        throw invalidOAuthClientCredentialsProfile();
    }
    const tokenUrl = validateOAuthTokenUrl(profile.tokenUrl);
    const clientId = validateOAuthCredential('clientId', profile.clientId, 256);
    const clientSecret = validateOAuthCredential('clientSecret', profile.clientSecret, 4096);
    const scopes = profile.scopes ?? [];
    if (!Array.isArray(scopes)
        || scopes.length > 32
        || scopes.some((scope) => typeof scope !== 'string'
            || scope.length === 0
            || scope.length > 512
            || scope.trim() !== scope
            || /[\u0000-\u001f\u007f\s]/.test(scope))) {
        throw invalidOAuthClientCredentialsProfile();
    }
    return {
        tokenUrl,
        clientId,
        clientSecret,
        scopes: [...scopes],
    };
}

function validateOAuthTokenUrl(value: unknown): string {
    if (typeof value !== 'string' || value.length === 0 || value.length > 2048) {
        throw invalidOAuthClientCredentialsProfile();
    }
    let parsed: URL;
    try {
        parsed = new URL(value);
    } catch {
        throw invalidOAuthClientCredentialsProfile();
    }
    const loopback = parsed.hostname === 'localhost'
        || parsed.hostname === '127.0.0.1'
        || parsed.hostname === '[::1]';
    if ((parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && loopback))
        || parsed.username
        || parsed.password
        || parsed.search
        || parsed.hash) {
        throw invalidOAuthClientCredentialsProfile();
    }
    return parsed.toString();
}

function validateOAuthCredential(name: string, value: unknown, maximum: number): string {
    if (typeof value !== 'string'
        || value.length === 0
        || value.length > maximum
        || value.trim() !== value
        || /[\u0000-\u001f\u007f]/.test(value)) {
        throw invalidOAuthClientCredentialsProfile();
    }
    return value;
}

function validateDuration(name: string, value: number, minimum: number, maximum: number): number {
    if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
        throw new OAuthClientCredentialsAuthError(`Invalid ${name} authentication setting`);
    }
    return value;
}

function parseOAuthTokenResponse(payload: unknown): { accessToken: string; expiresInSeconds: number } {
    if (!payload || typeof payload !== 'object') {
        throw new OAuthClientCredentialsAuthError(
            'OAuth client-credentials token exchange returned an invalid response',
        );
    }
    const source = payload as Record<string, unknown>;
    if (typeof source.access_token !== 'string'
        || source.access_token.length === 0
        || source.access_token.length > 1024 * 1024
        || (source.token_type !== undefined
            && (typeof source.token_type !== 'string' || source.token_type.toLowerCase() !== 'bearer'))
        || typeof source.expires_in !== 'number'
        || !Number.isSafeInteger(source.expires_in)
        || source.expires_in <= 0) {
        throw new OAuthClientCredentialsAuthError(
            'OAuth client-credentials token exchange returned an invalid response',
        );
    }
    return {
        accessToken: source.access_token,
        expiresInSeconds: source.expires_in,
    };
}

function invalidOAuthClientCredentialsProfile(): OAuthClientCredentialsAuthError {
    return new OAuthClientCredentialsAuthError('Invalid OAuth client-credentials profile');
}

function defaultFetch(): FetchLike {
    const fetchImpl = globalThis.fetch;
    if (!fetchImpl) {
        throw new OAuthClientCredentialsAuthError('No fetch implementation available for authentication');
    }
    return fetchImpl as unknown as FetchLike;
}
