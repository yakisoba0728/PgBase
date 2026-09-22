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

test('administrator schema workflows preserve exact text ID policies and preview snapshots', async () => {
  const calls=[];
  const recordId={kind:'text',minLength:4,maxLength:24,pattern:'^[A-Za-z0-9_-]+$',autogeneratePattern:'[A-Za-z0-9]{20}'};
  const collection={id:'collection-id',name:'articles',tableName:'articles',recordId,fields:[]};
  const definition={name:collection.name,tableName:collection.tableName,recordId,fields:[]};
  const authPolicy={passwordEnabled:true,minimumPasswordBytes:12,maximumPasswordBytes:128,passwordPattern:'',identityFields:['email'],mfaEnabled:false,mfaDurationSeconds:300,mfaRule:'',otpEnabled:false,otpDurationSeconds:300,otpMaxAttempts:5};
  const authCollection={name:'members',registrationEnabled:true,recordId,fields:[],rules:{list:null,view:'@request.auth.id = id',create:'',auth:''},policy:authPolicy,tokenTTLs:{session:3600}};
  const schema={format:'pgbase.schema/v1',collections:[{name:'articles',recordId,fields:[],userAccess:false,rules:{}},{name:'members',type:'auth',recordId,fields:[],userAccess:false,rules:{}}],authCollections:[authCollection]};
  const client=new PgBaseAdminClient('http://localhost',{fetch:async(url,init)=>{
    const path=new URL(url).pathname;
    calls.push({path,init});
    if(path==='/api/admin/collections') return Response.json({version:7,collections:{articles:collection}});
    if(path==='/api/admin/collections/plan') return Response.json({baseVersion:7,checksum:'create-checksum',definition,sql:[]});
    if(path==='/api/admin/collections/apply') return Response.json(collection);
    if(path==='/api/admin/schema/export') return Response.json(schema);
    if(path==='/api/admin/schema/import/preview') return Response.json({baseVersion:7,checksum:'import-checksum',policyChecksum:'policy',changes:[],document:schema});
    if(path==='/api/admin/schema/import/apply') return Response.json({operationId:'import-id',status:'committed'});
    throw new Error(`unexpected request: ${path}`);
  }});
  assert.deepEqual((await client.catalog()).collections.articles.recordId,recordId);
  const change={operation:'create',name:'articles',recordId:{...recordId},fields:[]};
  const pending=client.plan(change);
  change.recordId.pattern='^[a-z]+$';
  const plan=await pending;
  assert.deepEqual(plan.preview.definition.recordId,recordId);
  plan.preview.definition.recordId.maxLength=255;
  assert.deepEqual((await client.apply(plan)).recordId,recordId);
  const planned=JSON.parse(calls.find(call=>call.path==='/api/admin/collections/plan').init.body);
  const applied=JSON.parse(calls.find(call=>call.path==='/api/admin/collections/apply').init.body);
  assert.deepEqual(planned.recordId,recordId);
  assert.deepEqual(applied.recordId,recordId);
  assert.equal(applied.checksum,'create-checksum');

  const exported=await client.exportSchema();
  assert.deepEqual(exported.collections[0].recordId,recordId);
  assert.deepEqual(exported.authCollections[0],authCollection);
  const pendingImport=client.previewSchemaImport(exported);
  exported.collections[0].recordId.pattern='^[a-z]+$';
  exported.collections[1].recordId.pattern='^[a-z]+$';
  exported.authCollections[0].recordId.pattern='^[a-z]+$';
  exported.authCollections[0].policy.identityFields=['changed'];
  const importPlan=await pendingImport;
  importPlan.document.collections[0].recordId.maxLength=255;
  importPlan.document.authCollections[0].recordId.maxLength=255;
  await client.applySchemaImport(importPlan);
  const previewed=JSON.parse(calls.find(call=>call.path==='/api/admin/schema/import/preview').init.body);
  const imported=JSON.parse(calls.find(call=>call.path==='/api/admin/schema/import/apply').init.body);
  assert.deepEqual(previewed.collections[0].recordId,recordId);
  assert.deepEqual(imported.document.collections[0].recordId,recordId);
  assert.deepEqual(previewed.collections[1],schema.collections[1]);
  assert.deepEqual(imported.document.collections[1],schema.collections[1]);
  assert.deepEqual(previewed.authCollections[0],authCollection);
  assert.deepEqual(imported.document.authCollections[0],authCollection);
});

test('administrator declarative view updates bind the complete projection and filter', async () => {
  const calls=[];
  const client=new PgBaseAdminClient('http://localhost',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/plan')) return Response.json({baseVersion:3,checksum:'view-checksum',definition:{name:'published_articles',fields:[]},sql:[]});
    return Response.json({operationId:'view-update',status:'committed'});
  }});
  const view={source:'articles',projection:['title','published'],filter:{op:'eq',field:'published',value:true}};
  const change={operation:'update',name:'published_articles',view:structuredClone(view)};
  const pending=client.plan(change);
  change.view.projection.push('private_note');
  change.view.filter.value=false;
  await client.apply(await pending);
  assert.deepEqual(JSON.parse(calls[0].init.body),{operation:'update',name:'published_articles',view});
  assert.deepEqual(JSON.parse(calls[1].init.body),{operation:'update',name:'published_articles',view,baseVersion:3,checksum:'view-checksum'});
});

test('administrator schema scaffolds, truncate, and Apple secret generation use exact routes and CSRF', async () => {
  const calls=[];
  const client=new PgBaseAdminClient('http://localhost:8090',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin',email:'a@example.test'},expiresAt:'later',csrfToken:'csrf'});
    if(String(url).endsWith('/scaffolds')) return Response.json({base:{name:'base',userAccess:false,rules:{}},auth:{collection:{name:'members',type:'auth',userAccess:false,rules:{}},authCollection:{name:'members',registrationEnabled:false,rules:{},policy:{passwordEnabled:true}}},view:{name:'view',type:'view',userAccess:false,rules:{}}});
    if(String(url).endsWith('/truncate')) return Response.json({name:'posts',truncated:true,recordsDeleted:3,filesDeleted:1,relationsDeleted:2});
    return Response.json({secret:'generated-secret'},{headers:{'Cache-Control':'no-store'}});
  }});
  await client.resume();
  const scaffold = (await client.scaffolds()).auth;
  assert.equal(scaffold.collection.name,'members');
  assert.equal(scaffold.authCollection.name,scaffold.collection.name);
  assert.equal(scaffold.authCollection.policy.passwordEnabled,true);
  const truncated=await client.truncateCollection('posts');
  assert.equal(truncated.recordsDeleted,3);
  const generated=await client.generateAppleClientSecret({clientId:'web',teamId:'team',keyId:'key',privateKey:'private',duration:3600});
  assert.equal(generated.secret,'generated-secret');
  const truncate=calls.find(call=>call.url.endsWith('/collections/posts/truncate'));
  assert.deepEqual(JSON.parse(truncate.init.body),{name:'posts'});
  const apple=calls.at(-1);
  assert.equal(apple.url.endsWith('/settings/apple/generate-client-secret'),true);
  assert.equal(apple.init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.deepEqual(JSON.parse(apple.init.body),{clientId:'web',teamId:'team',keyId:'key',privateKey:'private',duration:3600});
});

test('administrator file storage API redacts secrets and protects mutations with CSRF', async () => {
  const calls=[];
  const settings={driver:'s3',bucket:'files',region:'us-east-1',endpoint:'https://s3.example.test',accessKey:'access',usePathStyle:true,secretConfigured:true,revision:4};
  const client=new PgBaseAdminClient('http://localhost:8090',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin',email:'a@example.test'},expiresAt:'later',csrfToken:'csrf'});
    if(init.method==='POST') return new Response(null,{status:204});
    return Response.json(settings);
  }});
  await client.resume();
  const loaded=await client.fileStorageSettings();
  assert.equal(loaded.secretConfigured,true);
  assert.equal('secretKey' in loaded,false);
  await client.updateFileStorage({...settings,secretKey:'replacement'});
  await client.testFileStorage();
  const storageCalls=calls.filter(call=>call.url.includes('/storage/files'));
  assert.equal(storageCalls[0].init.method,undefined);
  assert.equal(storageCalls[1].init.method,'PUT');
  assert.equal(storageCalls[1].init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.equal(JSON.parse(storageCalls[1].init.body).secretKey,'replacement');
  assert.equal(storageCalls[2].init.method,'POST');
  assert.equal(storageCalls[2].init.headers.get('X-PgBase-CSRF'),'csrf');
});

test('administrator backup storage API preserves revision and never expects returned secrets', async () => {
  const calls=[];
  const settings={driver:'s3',bucket:'backups',prefix:'daily',region:'us-east-1',endpoint:'https://s3.example.test',accessKey:'access',usePathStyle:true,retention:7,secretConfigured:true,revision:4};
  const client=new PgBaseAdminClient('http://localhost:8090',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin',email:'a@example.test'},expiresAt:'later',csrfToken:'csrf'});
    if(init.method==='POST') return new Response(null,{status:204});
    return Response.json(settings);
  }});
  await client.resume();
  const loaded=await client.backupStorageSettings();
  assert.equal(loaded.secretConfigured,true);
  assert.equal('secretKey' in loaded,false);
  const update={driver:'s3',bucket:'backups',prefix:'daily',region:'us-east-1',endpoint:'https://s3.example.test',accessKey:'access',usePathStyle:true,retention:7,revision:4,secretKey:'replacement'};
  await client.updateBackupStorage(update);
  await client.testBackupStorage();
  const storageCalls=calls.filter(call=>call.url.includes('/storage/backups'));
  assert.equal(storageCalls[0].init.method,undefined);
  assert.equal(storageCalls[1].init.method,'PUT');
  assert.equal(storageCalls[1].init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.deepEqual(JSON.parse(storageCalls[1].init.body),update);
  assert.equal(storageCalls[2].init.method,'POST');
  assert.equal(storageCalls[2].init.headers.get('X-PgBase-CSRF'),'csrf');
});

test('administrator storage migration API preserves identity across status, resume, and cancel', async () => {
  const calls=[]; const migration={id:'migration-id',status:'copying',copiedObjects:2,totalObjects:4};
  const client=new PgBaseAdminClient('http://localhost:8090',{fetch:async(url,init)=>{
    calls.push({url:String(url),init});
    if(String(url).endsWith('/session')) return Response.json({administrator:{id:'admin'},expiresAt:'later',csrfToken:'csrf'});
    if(init.method==='POST'&&(String(url).endsWith('/resume')||String(url).endsWith('/cancel'))) return Response.json(migration,{status:202});
    return Response.json(migration);
  }});
  await client.resume();
  const input={driver:'s3',bucket:'files',region:'us-east-1',endpoint:'https://s3.example.test',accessKey:'access',secretKey:'secret',usePathStyle:true};
  assert.equal((await client.previewFileStorageMigration(input)).totalObjects,4);
  assert.equal((await client.startFileStorageMigration(input,'11111111-1111-4111-8111-111111111111')).id,'migration-id');
  assert.equal(calls.at(-1).init.headers.get('PgBase-Operation-ID'),'11111111-1111-4111-8111-111111111111');
  await client.fileStorageMigration('migration/id');
  await client.resumeFileStorageMigration('migration/id');
  await client.cancelFileStorageMigration('migration/id');
  assert.equal(calls.at(-2).init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.equal(calls.at(-1).init.headers.get('X-PgBase-CSRF'),'csrf');
  assert.match(calls.at(-3).url,/migration%2Fid$/);
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
