package jsvm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	mathrand "math/rand/v2"
	"path/filepath"
	"regexp/syntax"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func (r *Runtime) installPocketBaseGlobals(vm *goja.Runtime, state *callState) error {
	host := vm.NewObject()
	for capability, methods := range map[string][]string{
		"http": {"send"}, "os": {"args", "getenv", "exit", "stat", "readFile", "writeFile", "readDir", "glob", "walk", "walkDir", "truncate", "getwd", "mkdir", "mkdirAll", "rename", "remove", "removeAll", "cmd"},
		"filesystem":   {"local", "fileFromPath", "fileFromBytes", "fileFromMultipart", "commitMultipart", "fileFromURL", "list", "copy", "read", "write", "delete", "open", "readChunk", "close", "beginWrite", "writeChunk", "commit", "abort"},
		"app-control":  {"isBootstrapped", "dataDir", "encryptionEnv", "isDev", "settings", "storeGet", "storeSet", "storeDelete", "reloadSettings", "createBackup", "restoreBackup", "restart", "runSystemMigrations", "runAppMigrations", "runAllMigrations", "transactionInfo", "cronList", "cronConfigure", "cronRun", "backupList", "backupRead", "backupDelete", "brokerClients", "brokerPublish"},
		"hooks-bypass": {"begin", "end"},
	} {
		object := vm.NewObject()
		for _, method := range methods {
			if err := object.Set(method, r.hostFunction(vm, state, capability, capability+"."+method)); err != nil {
				return err
			}
		}
		if err := host.Set(capability, object); err != nil {
			return err
		}
	}
	app := vm.NewObject()
	for _, method := range []string{"record.query", "record.findByID", "record.findByFilter", "record.findFirstByFilter", "record.findByIds", "record.findAll", "record.findFirstByData", "record.count", "record.canAccess", "record.expand", "record.expandMany", "model.save", "model.delete"} {
		if err := app.Set(method, r.hostFunction(vm, state, "records", "app."+method)); err != nil {
			return err
		}
	}
	for _, method := range []string{"log.query", "log.find", "log.stats", "log.deleteOld"} {
		if err := app.Set(method, r.hostFunction(vm, state, "app-control", "app."+method)); err != nil {
			return err
		}
	}
	if err := host.Set("app", app); err != nil {
		return err
	}
	authMail := vm.NewObject()
	if err := authMail.Set("send", r.hostFunction(vm, state, "mail", "authMail.send")); err != nil {
		return err
	}
	if err := host.Set("authMail", authMail); err != nil {
		return err
	}
	if err := vm.Set("__pgbaseCompatHost", host); err != nil {
		return err
	}
	if err := installSecurity(vm); err != nil {
		return err
	}
	if err := installFilepath(vm); err != nil {
		return err
	}
	if _, err := vm.RunString(pocketBaseCompatibilitySource); err != nil {
		return err
	}
	return r.installPocketBaseFilesystemGlobals(vm, state)
}

func installSecurity(vm *goja.Runtime) error {
	security := vm.NewObject()
	set := func(name string, value any) error { return security.Set(name, value) }
	for _, item := range []struct {
		name  string
		value any
	}{
		{"md5", func(value string) string { sum := md5.Sum([]byte(value)); return hex.EncodeToString(sum[:]) }},
		{"sha256", func(value string) string { sum := sha256.Sum256([]byte(value)); return hex.EncodeToString(sum[:]) }},
		{"sha512", func(value string) string { sum := sha512.Sum512([]byte(value)); return hex.EncodeToString(sum[:]) }},
		{"hs256", func(value, key string) string {
			h := hmac.New(sha256.New, []byte(key))
			_, _ = h.Write([]byte(value))
			return hex.EncodeToString(h.Sum(nil))
		}},
		{"hs512", func(value, key string) string {
			h := hmac.New(sha512.New, []byte(key))
			_, _ = h.Write([]byte(value))
			return hex.EncodeToString(h.Sum(nil))
		}},
		{"equal", func(left, right string) bool { return hmac.Equal([]byte(left), []byte(right)) }},
		{"randomString", func(length int) (string, error) {
			return secureRandomString(length, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
		}},
		{"randomStringWithAlphabet", func(length int, alphabet string) (string, error) { return secureRandomString(length, alphabet) }},
		{"pseudorandomString", func(length int) string {
			return pseudoRandomString(length, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
		}},
		{"pseudorandomStringWithAlphabet", func(length int, alphabet string) string { return pseudoRandomString(length, alphabet) }},
		{"createJWT", createJWT}, {"parseUnverifiedJWT", func(token string) (map[string]any, error) { return parseJWT(token, "") }}, {"parseJWT", parseJWT},
		{"encrypt", encryptString}, {"decrypt", decryptString},
	} {
		if err := set(item.name, item.value); err != nil {
			return err
		}
	}
	if err := set("randomStringByRegex", func(pattern string) (string, error) { return randomBySimpleRegex(pattern) }); err != nil {
		return err
	}
	return vm.Set("$security", security)
}

func secureRandomString(length int, alphabet string) (string, error) {
	if length < 0 || length > 4096 || len(alphabet) < 2 || len(alphabet) > 256 {
		return "", errors.New("invalid random string parameters")
	}
	out := make([]byte, length)
	sample := make([]byte, length)
	if _, err := rand.Read(sample); err != nil {
		return "", err
	}
	for i := range out {
		out[i] = alphabet[int(sample[i])%len(alphabet)]
	}
	return string(out), nil
}
func pseudoRandomString(length int, alphabet string) string {
	if length < 0 || length > 4096 || len(alphabet) < 2 {
		return ""
	}
	out := make([]byte, length)
	for i := range out {
		out[i] = alphabet[mathrand.IntN(len(alphabet))]
	}
	return string(out)
}
func randomBySimpleRegex(pattern string) (string, error) {
	if len(pattern) > 1024 {
		return "", errors.New("regex is too large")
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := writeRandomRegex(parsed, &out); err != nil {
		return "", err
	}
	if out.Len() > 4096 {
		return "", errors.New("generated string is too large")
	}
	return out.String(), nil
}
func writeRandomRegex(node *syntax.Regexp, out *strings.Builder) error {
	if out.Len() > 4096 {
		return errors.New("generated string is too large")
	}
	switch node.Op {
	case syntax.OpEmptyMatch:
	case syntax.OpLiteral:
		out.WriteString(string(node.Rune))
	case syntax.OpCapture:
		return writeRandomRegex(node.Sub[0], out)
	case syntax.OpCharClass:
		if len(node.Rune)%2 != 0 {
			return errors.New("invalid character class")
		}
		total := int64(0)
		for i := 0; i < len(node.Rune); i += 2 {
			total += int64(node.Rune[i+1] - node.Rune[i] + 1)
		}
		if total < 1 {
			return errors.New("empty character class")
		}
		choice, err := rand.Int(rand.Reader, big.NewInt(total))
		if err != nil {
			return err
		}
		offset := choice.Int64()
		for i := 0; i < len(node.Rune); i += 2 {
			size := int64(node.Rune[i+1] - node.Rune[i] + 1)
			if offset < size {
				out.WriteRune(node.Rune[i] + rune(offset))
				return nil
			}
			offset -= size
		}
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return writeRandomRegex(&syntax.Regexp{Op: syntax.OpCharClass, Rune: []rune{'0', '9', 'A', 'Z', 'a', 'z'}}, out)
	case syntax.OpAlternate:
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(node.Sub))))
		if err != nil {
			return err
		}
		return writeRandomRegex(node.Sub[choice.Int64()], out)
	case syntax.OpConcat:
		for _, sub := range node.Sub {
			if err := writeRandomRegex(sub, out); err != nil {
				return err
			}
		}
	case syntax.OpRepeat, syntax.OpQuest, syntax.OpPlus, syntax.OpStar:
		min, max := node.Min, node.Max
		if node.Op == syntax.OpQuest {
			min, max = 0, 1
		} else if node.Op == syntax.OpPlus {
			min, max = 1, 6
		} else if node.Op == syntax.OpStar {
			min, max = 0, 6
		}
		if min > 64 {
			return errors.New("regex repeat is too large")
		}
		if max < 0 || max > 64 {
			max = 64
		}
		count := min
		if max > min {
			choice, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
			if err != nil {
				return err
			}
			count += int(choice.Int64())
		}
		for range count {
			if err := writeRandomRegex(node.Sub[0], out); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported pattern operator %d", node.Op)
	}
	return nil
}
func createJWT(payload map[string]any, key string, seconds int) (string, error) {
	if len(key) < 1 || len(key) > 4096 || seconds < 0 {
		return "", errors.New("invalid JWT parameters")
	}
	if seconds > 0 {
		payload = cloneMap(payload)
		payload["exp"] = time.Now().Unix() + int64(seconds)
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}
func parseJWT(token, key string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid JWT")
	}
	if key != "" {
		h := hmac.New(sha256.New, []byte(key))
		_, _ = h.Write([]byte(parts[0] + "." + parts[1]))
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || !hmac.Equal(sig, h.Sum(nil)) {
			return nil, errors.New("invalid JWT signature")
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid JWT claims")
	}
	return value, nil
}
func encryptString(plain, key string) (string, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}
func decryptString(encoded, key string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	block, _ := aes.NewCipher(sum[:])
	gcm, _ := cipher.NewGCM(block)
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}
func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for k, v := range value {
		out[k] = v
	}
	return out
}

func installFilepath(vm *goja.Runtime) error {
	object := vm.NewObject()
	values := map[string]any{
		"base": filepath.Base, "clean": filepath.Clean, "dir": filepath.Dir, "ext": filepath.Ext,
		"fromSlash": filepath.FromSlash, "isAbs": filepath.IsAbs, "join": filepath.Join,
		"match": filepath.Match, "rel": filepath.Rel, "splitList": filepath.SplitList,
		"toSlash": filepath.ToSlash,
		"split":   func(path string) []string { dir, file := filepath.Split(path); return []string{dir, file} },
	}
	for name, value := range values {
		if err := object.Set(name, value); err != nil {
			return err
		}
	}
	return vm.Set("$filepath", object)
}

const pocketBaseCompatibilitySource = `(function(){
const H=__pgbaseCompatHost, unsupported=(name)=>()=>{throw {name:"PgBaseError",code:"platform_unsupported",operation:name}};
const BRAND=Symbol("pgbase.pb.kind"), STATE=new WeakMap();
const init=(target,kind,value={})=>{Object.defineProperty(target,BRAND,{value:kind});STATE.set(target,{...value});return target};
class Record{constructor(collection,data={}){init(this,"Record",{collection:typeof collection==="string"?collection:(collection&&collection.name)||"",id:data.id||"",data:{...data},original:{...data}})}get id(){return STATE.get(this).id}set id(v){STATE.get(this).id=String(v||"")}get collectionName(){return STATE.get(this).collection}get(k){return STATE.get(this).data[k]}set(k,v){STATE.get(this).data[k]=v;return this}unset(k){delete STATE.get(this).data[k];return this}load(v){Object.assign(STATE.get(this).data,v||{});return this}original(){return {...STATE.get(this).original}}getString(k){const v=this.get(k);return v==null?"":String(v)}getBool(k){return Boolean(this.get(k))}getInt(k){return Number.parseInt(this.get(k)||0,10)}getFloat(k){return Number(this.get(k)||0)}export(){const s=STATE.get(this);return {collection:s.collection,id:s.id,data:{...s.data}}}}
class Collection{constructor(value={}){if(typeof value==="string")value={name:value};init(this,"Collection",{...value,fields:[...(value.fields||[])]})}get id(){return STATE.get(this).id||""}get name(){return STATE.get(this).name||""}set name(v){STATE.get(this).name=String(v)}get fields(){return STATE.get(this).fields}export(){const s=STATE.get(this);return {...s,fields:[...s.fields]}}}
class Field{constructor(value={}){init(this,this.constructor.name,{...value});Object.assign(this,value)}export(){return {...STATE.get(this),...this}}}
for(const name of ["TextField","NumberField","BoolField","EmailField","URLField","DateField","SelectField","JSONField","FileField","RelationField","AutodateField"]){globalThis[name]=class extends Field{constructor(v={}){super(v);Object.defineProperty(this,"type",{value:name.replace("Field","").toLowerCase(),enumerable:true})}}}
class MailerMessage{constructor(value={}){init(this,"MailerMessage",{...value});Object.assign(this,value)}export(){return {...this}}};class RequestInfo{constructor(value={}){init(this,"RequestInfo",{...value});Object.assign(this,value)}export(){return {...this}}};class RequestEvent{constructor(value={}){init(this,"RequestEvent",{...value});Object.assign(this,value)}export(){return {...this}}};
globalThis.Record=Record;globalThis.Collection=Collection;globalThis.MailerMessage=MailerMessage;globalThis.RequestInfo=RequestInfo;globalThis.RequestEvent=RequestEvent;
const exp=(op,args)=>{const value={op,args};Object.defineProperty(value,BRAND,{value:"SQLExpression"});return Object.freeze(value)};
globalThis.$dbx={exp:(sql,params)=>exp("exp",[sql,params||{}]),hashExp:(v)=>exp("hashExp",[v]),not:(v)=>exp("not",[v]),and:(...v)=>exp("and",v),or:(...v)=>exp("or",v),in:(a,b)=>exp("in",[a,b]),notIn:(a,b)=>exp("notIn",[a,b]),like:(a,b)=>exp("like",[a,b]),orLike:(a,b)=>exp("orLike",[a,b]),notLike:(a,b)=>exp("notLike",[a,b]),orNotLike:(a,b)=>exp("orNotLike",[a,b]),exists:(v)=>exp("exists",[v]),notExists:(v)=>exp("notExists",[v]),between:(a,b,c)=>exp("between",[a,b,c]),notBetween:(a,b,c)=>exp("notBetween",[a,b,c])};
globalThis.$http={send:(v)=>H.http.send(v)};
globalThis.$os={get args(){return H.os.args({})},getenv:(name)=>H.os.getenv({name}),exit:(code=0)=>H.os.exit({code}),dirFS:(root)=>({root}),tempDir:()=>".",exec:(name,...args)=>H.os.cmd({name,args}),cmd:(name,...args)=>H.os.cmd({name,args}),stat:(path)=>H.os.stat({path}),readFile:(path)=>H.os.readFile({path}),writeFile:(path,data,mode)=>H.os.writeFile({path,data,mode}),readDir:(path)=>H.os.readDir({path}),truncate:(path,size)=>H.os.truncate({path,size}),getwd:()=>H.os.getwd({}),mkdir:(path,mode)=>H.os.mkdir({path,mode}),mkdirAll:(path,mode)=>H.os.mkdirAll({path,mode}),rename:(oldPath,newPath)=>H.os.rename({oldPath,newPath}),remove:(path)=>H.os.remove({path}),removeAll:(path)=>H.os.removeAll({path}),openRoot:(path)=>({root:path}),openInRoot:(root,path)=>H.os.readFile({path:$filepath.join(root,path)})};
globalThis.$filesystem={s3:unsupported("$filesystem.s3"),local:(root)=>H.filesystem.local({root}),fileFromPath:(path)=>H.filesystem.fileFromPath({path}),fileFromBytes:(bytes,name)=>H.filesystem.fileFromBytes({bytes,name}),fileFromMultipart:(field)=>H.filesystem.fileFromMultipart({field}),fileFromURL:(url,timeout)=>H.filesystem.fileFromURL({url,timeout}),list:(prefix="",cursor="",limit=100)=>H.filesystem.list({prefix,cursor,limit}),copy:(sourceKey,destinationKey)=>H.filesystem.copy({sourceKey,destinationKey}),read:(key)=>H.filesystem.read({key}),write:(key,data)=>H.filesystem.write({key,data}),delete:(key)=>H.filesystem.delete({key})};
const authMail=(kind,record,newEmail,otpId)=>H.authMail.send({kind,collection:record&&record.collectionName||record&&record.collection||"",recordId:record&&record.id||String(record||""),...(newEmail?{newEmail}:{}),...(otpId?{otpId}:{})});
globalThis.$mails={sendRecordPasswordReset:(record)=>authMail("passwordReset",record),sendRecordVerification:(record)=>authMail("verification",record),sendRecordChangeEmail:(record,newEmail)=>authMail("changeEmail",record,newEmail),sendRecordOTP:(record,otpId)=>authMail("otp",record,"",otpId),sendRecordAuthAlert:(record)=>authMail("authAlert",record)};
class ApiError extends Error{constructor(status=500,message="",data={}){super(message);this.status=status;this.data=data}}; globalThis.ApiError=ApiError; globalThis.NotFoundError=class extends ApiError{constructor(m,d){super(404,m,d)}};globalThis.BadRequestError=class extends ApiError{constructor(m,d){super(400,m,d)}};globalThis.ForbiddenError=class extends ApiError{constructor(m,d){super(403,m,d)}};globalThis.UnauthorizedError=class extends ApiError{constructor(m,d){super(401,m,d)}};globalThis.TooManyRequestsError=class extends ApiError{constructor(m,d){super(429,m,d)}};globalThis.InternalServerError=class extends ApiError{constructor(m,d){super(500,m,d)}};
const principal=(e)=>e&&(e.principal||(e.requestInfo&&e.requestInfo.auth));
const middleware=(check,code)=>(next)=>(e)=>{if(!check(e))throw new ApiError(code,code===401?"authentication required":"forbidden");return next(e)};
const routeResponse=(response)=>response&&typeof response==="object"?response:{status:204};
const staticRoot=(root)=>typeof root==="string"?root:(root&&typeof root.root==="string"?root.root:"");
globalThis.$apis={static:(root)=>(e)=>({status:200,headers:{},transport:{static:{root:staticRoot(root),path:(e&&e.pathValues&&(e.pathValues.path||e.pathValues.$))||""}}}),requireGuestOnly:middleware((e)=>!principal(e),403),requireAuth:(...collections)=>middleware((e)=>{const p=principal(e);return !!p&&(!collections.length||collections.includes(p.collection||p.kind))},401),requireSuperuserAuth:middleware((e)=>{const p=principal(e);return !!p&&(p.kind==="admin"||p.superuser===true)},401),requireSuperuserOrOwnerAuth:(owner)=>middleware((e)=>{const p=principal(e);return !!p&&(p.kind==="admin"||p.superuser===true||p.id===owner)},403),skipSuccessActivityLog:(next)=>{next.pgbaseSkipSuccessActivityLog=true;return next},gzip:(next)=>(e)=>{const response=routeResponse(next(e));response.transport=Object.assign({},response.transport,{gzip:true});return response},bodyLimit:(limit,next)=>(e)=>{if(JSON.stringify(e&&e.body||null).length>limit)throw new ApiError(413,"request body too large");return next(e)},recordAuthResponse:(e,record,method,meta)=>{e.response={record,authMethod:method,meta};return e.response},enrichRecord:(record)=>record,enrichRecords:(records)=>records};
const recordInput=(record)=>{if(record&&STATE.get(record)){const value=record.export();return {collection:value.collection,id:value.id,input:value.data}};return {collection:record.collection||record.collectionName,id:record.id,input:record.input||record.data||record}};
const appStore=Object.freeze({get:(key)=>H["app-control"].storeGet({key}),set:(key,value)=>H["app-control"].storeSet({key,value}),delete:(key)=>H["app-control"].storeDelete({key})});
const appCron=Object.freeze({list:()=>H["app-control"].cronList({}),configure:(id,schedule,timezone="UTC",enabled=true)=>H["app-control"].cronConfigure({id,schedule,timezone,enabled}),run:(id)=>H["app-control"].cronRun({id})});
const backupFS=Object.freeze({list:()=>H["app-control"].backupList({}),read:(id)=>H["app-control"].backupRead({id}),delete:(id)=>H["app-control"].backupDelete({id})});
const broker=Object.freeze({clients:()=>H["app-control"].brokerClients({}),publish:(topic,data)=>H["app-control"].brokerPublish({topic,data})});
const appRecord=(method,value)=>H.app[method](value);
globalThis.$app={isBootstrapped:()=>H["app-control"].isBootstrapped({}),bootstrap:unsupported("$app.bootstrap"),resetBootstrapState:unsupported("$app.resetBootstrapState"),dataDir:()=>H["app-control"].dataDir({}),encryptionEnv:()=>H["app-control"].encryptionEnv({}),isDev:()=>H["app-control"].isDev({}),settings:()=>H["app-control"].settings({}),store:()=>appStore,cron:()=>appCron,subscriptionsBroker:unsupported("$app.subscriptionsBroker"),newBackupsFilesystem:()=>backupFS,reloadSettings:()=>H["app-control"].reloadSettings({}),createBackup:()=>H["app-control"].createBackup({}),restoreBackup:(id)=>H["app-control"].restoreBackup({id}),restart:()=>H["app-control"].restart({}),runSystemMigrations:()=>H["app-control"].runSystemMigrations({}),runAppMigrations:()=>H["app-control"].runAppMigrations({}),runAllMigrations:()=>H["app-control"].runAllMigrations({}),isTransactional:()=>false,transactionInfo:()=>H["app-control"].transactionInfo({}),unsafeWithoutHooks:unsupported("$app.unsafeWithoutHooks"),recordQuery:(collection,filter,sort,limit)=>appRecord("record.query",{collection,filter,sort,limit}),findRecordsByFilter:(collection,filter,sort,limit)=>require("pgbase").records.list({collection,filter,sort,limit}),findFirstRecordByFilter:(collection,filter)=>require("pgbase").records.list({collection,filter,limit:1}).items[0],findRecordById:(collection,id)=>require("pgbase").records.get({collection,id}),findRecordsByIds:(collection,ids)=>appRecord("record.findByIds",{collection,ids}).items.map(v=>new Record(collection,v.data||v)),findAllRecords:(collection)=>appRecord("record.findAll",{collection}).items.map(v=>new Record(collection,v.data||v)),findFirstRecordByData:(collection,field,value)=>new Record(collection,appRecord("record.findFirstByData",{collection,field,value})),countRecords:(collection,filter)=>appRecord("record.count",{collection,filter}),canAccessRecord:(record,requestInfo,rule)=>appRecord("record.canAccess",{collection:record.collectionName,id:record.id,rule}),expandRecord:(record,expand)=>new Record(record.collectionName,appRecord("record.expand",{collection:record.collectionName,id:record.id,expand}).data),expandRecords:(records,expand)=>appRecord("record.expandMany",{collection:records[0]&&records[0].collectionName,ids:records.map(r=>r.id),expand}).items.map(v=>new Record(records[0].collectionName,v)),logQuery:(filter)=>appRecord("log.query",filter||{}),findLogById:(id)=>appRecord("log.find",{id}),logsStats:()=>appRecord("log.stats",{}),deleteOldLogs:(before)=>appRecord("log.deleteOld",{to:before}),save:(record)=>record.id?require("pgbase").records.update(recordInput(record)):require("pgbase").records.create(recordInput(record)),delete:(record)=>require("pgbase").records.delete({collection:record.collection||record.collectionName,id:record.id}),logger:()=>require("pgbase").log,mailer:()=>require("pgbase").mail,db:()=>require("pgbase").database,filesystem:()=>$filesystem};
globalThis.$app.subscriptionsBroker=()=>broker;
globalThis.$app.unsafeWithoutHooks=(fn)=>{if(typeof fn!=="function")throw new TypeError("callback required");H["hooks-bypass"].begin({});try{return fn()}finally{H["hooks-bypass"].end({})}};
})();`
