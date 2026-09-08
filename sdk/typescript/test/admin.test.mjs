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
  await client.logout();await client.queueJob('job');assert.equal(calls.at(-1).init.headers.get('X-PgBase-CSRF'),'');
});

test('admin rejects unreadable success instead of claiming an applied schema', async()=>{
  const client=new PgBaseAdminClient('http://localhost',{fetch:async()=>new Response('invalid',{status:200})});
  await assert.rejects(client.catalog(),error=>error instanceof AdminAPIError&&error.code==='invalid_response_body');
});
