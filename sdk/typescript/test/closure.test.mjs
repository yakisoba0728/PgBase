import assert from 'node:assert/strict';
import test from 'node:test';
import { PgBaseClient } from '../dist/index.js';

test('shared topic updates serialize, roll back rejected topics and preserve whitespace', async () => {
  let remote = [], active = 0, maximum = 0, write;
  const client = new PgBaseClient('http://pgbase.test', { fetch: async (_url, init) => {
    if (init.method === 'POST') {
      maximum = Math.max(maximum, ++active);
      const topics = JSON.parse(init.body).subscriptions;
      await new Promise(resolve => setTimeout(resolve, 5));
      active--;
      if (topics.includes('invalid/topic/extra')) return Response.json({error:{code:'invalid_subscription'}}, {status:400});
      remote = topics;
      return new Response(null, {status:204});
    }
    return new Response(new ReadableStream({start(controller) {
      write = frame => controller.enqueue(new TextEncoder().encode(frame));
      write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
      init.signal.addEventListener('abort', () => controller.close(), {once:true});
    }}));
  }});
  try {
    await client.realtime.subscribe('notes/*', () => {});
    const topic = "notes?filter=title%3D%27x%27 ";
    let received = 0;
    const invalid = client.realtime.subscribe('invalid/topic/extra', () => {});
    const rejected = assert.rejects(invalid);
    await Promise.all([rejected, client.realtime.subscribe('notes/one', () => {}), client.realtime.subscribe(topic, () => received++)]);
    assert.equal(maximum, 1);
    assert.deepEqual(remote, ['notes/*', 'notes/one', topic]);
    write(`event: ${topic}\ndata: {"action":"create","record":{"id":"one"}}\n\n`);
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(received, 1);
  } finally { await client.realtime.unsubscribe(); }
});

test('job configuration forwards an explicit timezone', async () => {
  const { PgBaseAdminClient } = await import('../dist/index.js');
  let body;
  const client = new PgBaseAdminClient('http://pgbase.test', {fetch: async (_url, init) => {
    body = JSON.parse(init.body);
    return new Response(null, {status:204});
  }});
  await client.configureJob('daily', '0 0 * * *', false, undefined, 'Asia/Seoul');
  assert.equal(body.timezone, 'Asia/Seoul');
});

for (const realmLogout of [false, true]) {
  test(`logout waits for token rotation and sends replacement (${realmLogout ? 'realm' : 'public'})`, async () => {
    let release, logoutToken;
    const pending = new Promise(resolve => release = resolve);
    const client = new PgBaseClient('http://pgbase.test', {fetch: async (url, init) => {
      if (String(url).endsWith('/refresh')) return pending;
      logoutToken = new Headers(init.headers).get('authorization');
      return new Response(null, {status:204});
    }});
    client.auth.save('old');
    const refreshing = client.realm('members').refresh();
    const loggingOut = realmLogout ? client.realm('members').logout() : client.logout();
    assert.equal(logoutToken, undefined);
    release(Response.json({token:'replacement',expiresAt:'later',user:{id:'one',email:'a@example.test',createdAt:'then'}}));
    await Promise.all([refreshing, loggingOut]);
    assert.equal(logoutToken, 'Bearer replacement');
    assert.equal(client.auth.token, null);
  });
  test(`explicit newer login survives pending rotation/logout (${realmLogout ? 'realm' : 'public'})`, async () => {
    let release, logouts = 0;
    const pending = new Promise(resolve => release = resolve);
    const client = new PgBaseClient('http://pgbase.test', {fetch: async (url) => {
      if (String(url).endsWith('/refresh')) return pending;
      logouts++;
      return new Response(null, {status:204});
    }});
    client.auth.save('old');
    const refreshing = client.realm('members').refresh();
    const loggingOut = realmLogout ? client.realm('members').logout() : client.logout();
    client.auth.save('explicit-new-login');
    release(Response.json({token:'replacement',expiresAt:'later',user:{id:'one',email:'a@example.test',createdAt:'then'}}));
    await Promise.all([refreshing, loggingOut]);
    assert.equal(logouts, 0);
    assert.equal(client.auth.token, 'explicit-new-login');
  });
}

test('unsupported metadata projection rejects before HTTP and verification sends JSON', async () => {
  const calls = [];
  const client = new PgBaseClient('http://pgbase.test', {fetch: async (url, init) => {
    calls.push({url,init}); return new Response(null,{status:204});
  }});
  await assert.rejects(client.records('notes').events({fields:'id,title'}).next(), error => error.code === 'unsupported_event_projection');
  assert.equal(calls.length, 0);
  await client.requestVerification();
  assert.equal(calls[0].init.body, '{}');
  assert.match(new Headers(calls[0].init.headers).get('content-type'), /application\/json/);
});

test('schema import applies exact hook-approved preview document, retaining large integers', async () => {
  const {PgBaseAdminClient} = await import('../dist/index.js');
  let apply;
  const approved = '{"collections":[],"marker":"hook-approved","n":900719925474099312345,"nested":{"document":"wrong","quoted":"a\\\"b"}}';
  const client = new PgBaseAdminClient('http://pgbase.test', {fetch: async (url, init) => {
    if (String(url).endsWith('/preview')) return new Response(`{"baseVersion":1,"checksum":"approved","policyChecksum":"policy","changes":[],"document":${approved}}`);
    apply = init.body;
    return Response.json({operationId:'done',status:'committed'});
  }});
  const plan = await client.previewSchemaImportJSON('{"collections":[],"marker":"original"}');
  plan.document.marker = 'tampered';
  await client.applySchemaImport(plan);
  assert.ok(apply.includes(`"document":${approved}`));
  assert.ok(apply.includes('900719925474099312345'));
  assert.equal(JSON.parse(apply).checksum, 'approved');
});

test('logout revokes an in-memory rotated token even when refresh persistence fails', async () => {
  const {AuthStore} = await import('../dist/index.js');
  let release, logoutToken;
  const pending = new Promise(resolve => release = resolve);
  const auth = new AuthStore({get:()=> 'old',set:()=> {throw new Error('disk unavailable');},clear:()=>{}});
  const client = new PgBaseClient('http://pgbase.test', {auth,fetch:async(url,init)=>{
    if(String(url).endsWith('/refresh')) return pending;
    logoutToken = new Headers(init.headers).get('authorization');
    return new Response(null,{status:204});
  }});
  const rejected = assert.rejects(client.realm('members').refresh(), /disk unavailable/);
  const loggingOut = client.logout();
  release(Response.json({token:'replacement',expiresAt:'later',user:{id:'one',email:'a@example.test',createdAt:'then'}}));
  await Promise.all([rejected,loggingOut]);
  assert.equal(logoutToken,'Bearer replacement');
  assert.equal(auth.token,null);
});
