const assert = require('assert');
const {
  ArtificialFlowClient,
  OAuthClientCredentialsAuthError,
  OAuthClientCredentialsAuthProvider,
} = require('../dist');

const oauthProfile = {
  type: 'oauth-client-credentials',
  tokenUrl: 'https://iam.example/realms/artificialflow/protocol/openid-connect/token',
  clientId: 'artificialflow-sdk-client',
  clientSecret: 'private-client-secret',
  scopes: ['openid'],
};

function response(status, payload, statusText = status < 400 ? 'OK' : 'Error') {
  return {
    ok: status < 400,
    status,
    statusText,
    headers: { get: () => 'application/json' },
    text: async () => payload === undefined ? '' : JSON.stringify(payload),
  };
}

async function testOAuthClientCredentialsExchangeAndCache() {
  let now = 1_700_000_000_000;
  const calls = [];
  const tokens = ['oauth-one', 'oauth-two'];
  const provider = new OAuthClientCredentialsAuthProvider({
    type: 'oauth-client-credentials',
    profile: oauthProfile,
    clock: () => now,
    refreshSkewMs: 10_000,
    fetch: async (url, init) => {
      calls.push({ url, init });
      return response(200, {
        access_token: tokens.shift(),
        token_type: 'Bearer',
        expires_in: 60,
      });
    },
  });

  assert.strictEqual(await provider.getToken(), 'oauth-one');
  assert.strictEqual(await provider.getToken(), 'oauth-one');
  assert.strictEqual(calls.length, 1);
  assert.strictEqual(calls[0].url, oauthProfile.tokenUrl);
  assert.strictEqual(calls[0].init.method, 'POST');
  assert.deepStrictEqual(calls[0].init.headers, {
    Accept: 'application/json',
    'Content-Type': 'application/x-www-form-urlencoded',
  });
  const form = new URLSearchParams(calls[0].init.body);
  assert.strictEqual(form.get('grant_type'), 'client_credentials');
  assert.strictEqual(form.get('client_id'), oauthProfile.clientId);
  assert.strictEqual(form.get('client_secret'), oauthProfile.clientSecret);
  assert.strictEqual(form.get('scope'), 'openid');

  now += 50_001;
  assert.strictEqual(await provider.getToken(), 'oauth-two');
  assert.strictEqual(calls.length, 2);
}

async function testOAuthSingleFlightAndFailureRecovery() {
  let release;
  let calls = 0;
  const provider = new OAuthClientCredentialsAuthProvider({
    type: 'oauth-client-credentials',
    profile: oauthProfile,
    fetch: async () => {
      calls += 1;
      await new Promise((resolve) => { release = resolve; });
      return response(200, {
        access_token: 'shared-access-token',
        token_type: 'bearer',
        expires_in: 60,
      });
    },
  });

  const pending = [
    provider.getToken(),
    provider.getToken(),
    provider.getToken(),
  ];
  await Promise.resolve();
  assert.strictEqual(calls, 1);
  release();
  assert.deepStrictEqual(await Promise.all(pending), [
    'shared-access-token',
    'shared-access-token',
    'shared-access-token',
  ]);

  let attempt = 0;
  let rejectedBodyReads = 0;
  const retrying = new OAuthClientCredentialsAuthProvider({
    type: 'oauth-client-credentials',
    profile: oauthProfile,
    fetch: async () => {
      attempt += 1;
      return attempt === 1
        ? {
          ...response(401, {}),
          text: async () => {
            rejectedBodyReads += 1;
            return JSON.stringify({
              error: 'invalid_client',
              client_secret: oauthProfile.clientSecret,
            });
          },
        }
        : response(200, {
          access_token: 'recovered-token',
          expires_in: 60,
        });
    },
  });
  await assert.rejects(
    () => retrying.getToken(),
    (error) => {
      assert.ok(error instanceof OAuthClientCredentialsAuthError);
      assert.strictEqual(error.status, 401);
      assert.strictEqual(error.message, 'OAuth client-credentials token exchange was rejected');
      assert.ok(!error.message.includes(oauthProfile.clientSecret));
      return true;
    },
  );
  assert.strictEqual(rejectedBodyReads, 0);
  assert.strictEqual(await retrying.getToken(), 'recovered-token');
  assert.strictEqual(attempt, 2);
}

async function testOAuthClientCredentialsValidationAndClientIntegration() {
  assert.throws(
    () => new OAuthClientCredentialsAuthProvider({
      type: 'oauth-client-credentials',
      profile: { ...oauthProfile, tokenUrl: 'http://iam.example/token' },
      fetch: async () => response(200, {}),
    }),
    (error) => error instanceof OAuthClientCredentialsAuthError
      && error.message === 'Invalid OAuth client-credentials profile',
  );

  const invalidResponses = [
    {},
    { access_token: '', expires_in: 60 },
    { access_token: 'token', token_type: 'Basic', expires_in: 60 },
    { access_token: 'token', expires_in: 0 },
    { access_token: 'token', expires_in: '60' },
  ];
  for (const payload of invalidResponses) {
    const provider = new OAuthClientCredentialsAuthProvider({
      type: 'oauth-client-credentials',
      profile: oauthProfile,
      fetch: async () => response(200, payload),
    });
    await assert.rejects(
      () => provider.getToken(),
      (error) => error instanceof OAuthClientCredentialsAuthError
        && error.message === 'OAuth client-credentials token exchange returned an invalid response',
    );
  }

  let rejectedBodyReads = 0;
  const rejected = new OAuthClientCredentialsAuthProvider({
    type: 'oauth-client-credentials',
    profile: oauthProfile,
    fetch: async () => ({
      ...response(401, {}),
      text: async () => {
        rejectedBodyReads += 1;
        return JSON.stringify({ client_secret: oauthProfile.clientSecret });
      },
    }),
  });
  await assert.rejects(
    () => rejected.getToken(),
    (error) => error instanceof OAuthClientCredentialsAuthError
      && error.status === 401
      && !error.message.includes(oauthProfile.clientSecret),
  );
  assert.strictEqual(rejectedBodyReads, 0);

  const calls = [];
  const client = new ArtificialFlowClient({
    baseUrl: 'https://api.artificialflow.example.io',
    auth: { type: 'oauth-client-credentials', profile: oauthProfile },
    fetch: async (url, init) => {
      calls.push({ url, init });
      if (url === oauthProfile.tokenUrl) {
        return response(200, {
          access_token: 'oauth-client-token',
          token_type: 'Bearer',
          expires_in: 60,
        });
      }
      return response(200, { status: 'ok' });
    },
  });
  await client.health();
  assert.strictEqual(calls.length, 2);
  assert.strictEqual(calls[1].init.headers.Authorization, 'Bearer oauth-client-token');
}

async function testClientIntegrationAndRawModeSwitch() {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url, init });
    if (url === oauthProfile.tokenUrl) {
      return response(200, {
        access_token: 'profile-access-token',
        token_type: 'Bearer',
        expires_in: 60,
      });
    }
    return response(200, { status: 'ok' });
  };
  const client = new ArtificialFlowClient({
    baseUrl: 'https://api.artificialflow.example.io',
    fetch,
    auth: {
      type: 'oauth-client-credentials',
      profile: oauthProfile,
    },
  });

  await client.health();
  assert.strictEqual(calls.length, 2);
  assert.strictEqual(calls[1].init.headers.Authorization, 'Bearer profile-access-token');

  client.setToken('raw-migration-token');
  await client.health();
  assert.strictEqual(calls.length, 3);
  assert.strictEqual(calls[2].init.headers.Authorization, 'Bearer raw-migration-token');

  assert.throws(
    () => new ArtificialFlowClient({
      token: 'raw-token',
      auth: { type: 'oauth-client-credentials', profile: oauthProfile },
      fetch,
    }),
    /options\.token and options\.auth cannot be used together/,
  );
}

async function testWorkerReusesAccessToken() {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url, init });
    if (url === oauthProfile.tokenUrl) {
      return response(200, {
        access_token: 'worker-access-token',
        expires_in: 60,
      });
    }
    if (url.endsWith('/jobs/activate')) {
      return response(200, {
        jobs: [{
          key: 'job-1',
          type: 'payment',
          processInstanceKey: 'instance-1',
          elementInstanceKey: 'element-1',
          processDefinitionKey: 'definition-1',
          elementId: 'charge',
          worker: 'worker-1',
          retries: 3,
          state: 'ACTIVATED',
          createdAt: '2026-01-01T00:00:00Z',
          updatedAt: '2026-01-01T00:00:00Z',
        }],
      });
    }
    return response(204);
  };
  const client = new ArtificialFlowClient({
    baseUrl: 'https://api.artificialflow.example.io',
    fetch,
    auth: { type: 'oauth-client-credentials', profile: oauthProfile },
  });
  const worker = client.createWorker(
    'payment',
    async () => ({ paid: true }),
    { workerName: 'worker-1' },
  );

  assert.strictEqual(await worker.runOnce(), 1);
  assert.strictEqual(calls.filter((call) => call.url === oauthProfile.tokenUrl).length, 1);
  const apiCalls = calls.filter((call) => call.url !== oauthProfile.tokenUrl);
  assert.strictEqual(apiCalls.length, 2);
  assert.ok(apiCalls.every(
    (call) => call.init.headers.Authorization === 'Bearer worker-access-token',
  ));
}

async function main() {
  await testOAuthClientCredentialsExchangeAndCache();
  await testOAuthSingleFlightAndFailureRecovery();
  await testOAuthClientCredentialsValidationAndClientIntegration();
  await testClientIntegrationAndRawModeSwitch();
  await testWorkerReusesAccessToken();
  console.log('auth.test.js passed');
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
