import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PgBaseAdminClient, AdminAPIError } from '../dist/index.js';

test('admin preview snapshots input and retries exact operation while retaining CSRF only in memory', async () => {
  const calls=[];
  const client=new PgBaseAdminClient('http://localhost:8090', {fetch:async (url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin',email:'a@example.test'},expiresAt:'later',csrfToken:'csrf'});
    if(String(url).endsWith('/plan')) return Response.json({baseVersion:3,checksum:'digest',definition:{name:'posts',fields:[]},sql:['ALTER']});
    if(String(url).endsWith('/apply')) return Response.json({operationId:'receipt',status:'committed'});
    return new Response(null,{status:204});
  }});
  const session=await client.resume(); assert.equal(session.csrfToken,undefined);
  const change={operation:'update',name:'posts',fields:[{id:'kept-field-id',name:'renamed',type:'text',options:{minLength:2,format:'email'}}]};
  const pending=client.plan(change);change.fields[0].name='later-edit';
  const plan=await pending;plan.preview.checksum='tampered';
  await client.apply(plan);await client.apply(plan);
  const applies=calls.filter(c=>c.url.endsWith('/apply'));
  assert.equal(applies[0].init.body,applies[1].init.body);
  const sent=JSON.parse(applies[0].init.body);
  assert.equal(sent.fields[0].name,'renamed');assert.equal(sent.checksum,'digest');assert.equal(sent.fields[0].id,'kept-field-id');
  assert.equal(applies[0].init.headers.get('PgBase-Operation-ID'),applies[1].init.headers.get('PgBase-Operation-ID'));
  assert.equal(applies[0].init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.equal(applies[0].init.headers.has('Authorization'),false);assert.equal(applies[0].init.credentials,'include');
	await client.plan({operation:'create',name:'post_titles',view:{sql:'SELECT id, title FROM app.posts'}});
	const sqlView=JSON.parse(calls.filter(c=>c.url.endsWith('/plan')).at(-1).init.body);
	assert.deepEqual(sqlView.view,{sql:'SELECT id, title FROM app.posts'});
	await client.plan({operation:'update',name:'post_titles',view:{sql:'SELECT id, upper(title) AS title FROM app.posts'}});
	const sqlViewUpdate=JSON.parse(calls.filter(c=>c.url.endsWith('/plan')).at(-1).init.body);
	assert.deepEqual(sqlViewUpdate,{operation:'update',name:'post_titles',view:{sql:'SELECT id, upper(title) AS title FROM app.posts'}});
  await client.logout();await client.queueJob('job');assert.equal(calls.at(-1).init.headers.get('X-PgBase-CSRF'),'');
});

test('administrator impersonation uses CSRF and preserves the explicit origin marker', async () => {
  const calls=[];
  const client=new PgBaseAdminClient('http://localhost:8090',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin',email:'a@example.test'},expiresAt:'later',csrfToken:'csrf'});
    return Response.json({token:'temporary',user:{id:'user'},expiresAt:'later',impersonated:true,impersonatingAdministratorId:'admin'});
  }});
  await client.resume();
  const session=await client.impersonateUser('user',120);
  assert.equal(session.impersonated,true);
  assert.equal(session.impersonatingAdministratorId,'admin');
  assert.equal(calls.at(-1).init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.deepEqual(JSON.parse(calls.at(-1).init.body),{durationSeconds:120});
});

test('admin rejects unreadable success instead of claiming an applied schema', async()=>{
  const client=new PgBaseAdminClient('http://localhost',{fetch:async()=>new Response('invalid',{status:200})});
  await assert.rejects(client.catalog(),error=>error instanceof AdminAPIError&&error.code==='invalid_response_body');
});

test('raw schema export preview and apply preserve integers beyond JavaScript precision', async()=>{
  const raw='{"format":"pgbase.schema/v1","collections":[{"name":"precise","fields":[{"name":"counter","type":"bigint","default":9007199254740993}],"userAccess":false,"rules":{}}]}';
  const calls=[];
  const client=new PgBaseAdminClient('http://localhost',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/schema/export')) return new Response(raw,{headers:{'Content-Type':'application/json'}});
    if(String(url).endsWith('/import/preview')) return Response.json({baseVersion:4,checksum:'exact',policyChecksum:'policy',changes:[],document:JSON.parse(raw)});
    if(String(url).endsWith('/import/apply')) return Response.json({operationId:'done',status:'committed'});
    throw new Error('unexpected request');
  }});
  const exported=await client.exportSchemaJSON(); assert.equal(exported,raw);
  const plan=await client.previewSchemaImportJSON(exported);
  plan.document.collections[0].fields[0].default=1;
  const result=await client.applySchemaImport(plan);
  assert.equal(result.version,undefined); assert.equal(result.applied,undefined);
  const preview=calls.find(c=>c.url.endsWith('/import/preview')); const apply=calls.find(c=>c.url.endsWith('/import/apply'));
  assert.equal(preview.init.body,raw);
  assert.match(apply.init.body,/9007199254740993/);
  assert.doesNotMatch(apply.init.body,/"default":1[,}]/);
  assert.equal(apply.init.headers.get('PgBase-Operation-ID'),plan.operationId);
  assert.equal(apply.init.headers.get('Idempotency-Key'),plan.idempotencyKey);
});
