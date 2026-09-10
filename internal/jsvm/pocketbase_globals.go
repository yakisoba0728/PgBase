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
		"http": {"send"}, "os": {"args", "getenv", "exit", "tempDir", "stat", "readFile", "writeFile", "readDir", "glob", "walk", "walkDir", "truncate", "getwd", "mkdir", "mkdirAll", "rename", "remove", "removeAll", "cmd"},
		"filesystem":   {"local", "localWrite", "localCopy", "localStat", "localRead", "localReadDir", "localWalk", "localRemove", "localRemoveAll", "fileFromPath", "fileFromBytes", "fileFromMultipart", "commitMultipart", "fileFromURL", "list", "copy", "read", "write", "delete", "deletePrefix", "exists", "attributes", "open", "readChunk", "close", "beginWrite", "writeChunk", "commit", "abort"},
		"app-control":  {"isBootstrapped", "dataDir", "encryptionEnv", "isDev", "settings", "storeGet", "storeSet", "storeDelete", "reloadSettings", "createBackup", "restoreBackup", "restart", "runSystemMigrations", "runAppMigrations", "runAllMigrations", "transactionInfo", "cronList", "cronConfigure", "cronRun", "backupList", "backupRead", "backupDelete", "brokerClients", "brokerPublish", "schemaExport", "schemaPreview", "schemaApply", "collectionSave", "collectionValidate", "collectionDelete", "collectionSync", "collectionTruncate", "databaseTables", "databaseColumns", "databaseIndexes", "databaseTransaction"},
		"hooks-bypass": {"begin", "end", "collectionSaveNoValidate"},
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
	for _, method := range []string{"collection.get", "collection.cachedGet", "collection.list", "collection.nameUnique", "collection.references", "authAux.list", "authAux.delete"} {
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
	apis := vm.NewObject()
	for _, item := range []struct{ name, capability string }{{"recordAuthResponse", "app-control"}, {"enrichRecord", "records"}, {"enrichRecords", "records"}} {
		if err := apis.Set(item.name, r.hostFunction(vm, state, item.capability, "apis."+item.name)); err != nil {
			return err
		}
	}
	if err := host.Set("apis", apis); err != nil {
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
const FIELD_TYPES={NumberField:"number",BoolField:"bool",TextField:"text",URLField:"url",EmailField:"email",EditorField:"editor",PasswordField:"password",DateField:"date",AutodateField:"autodate",JSONField:"json",RelationField:"relation",SelectField:"select",FileField:"file",GeoPointField:"geoPoint"};
for(const [name,type] of Object.entries(FIELD_TYPES)){globalThis[name]=class extends Field{constructor(v={}){super(v);Object.defineProperty(this,"type",{value:type,enumerable:true})}}}
class FieldsList extends Array{constructor(values=[]){super();init(this,"FieldsList",{});for(const value of values||[])this.push(value instanceof Field?value:new Field(value))}getByName(name){return this.find((f)=>f&&f.name===name)}add(...fields){this.push(...fields);return this}removeByName(name){const i=this.findIndex((f)=>f&&f.name===name);if(i>=0)this.splice(i,1);return this}export(){return this.map((f)=>f&&typeof f.export==="function"?f.export():f)}}
class Command{constructor(value={}){init(this,"Command",{});Object.assign(this,value)}}
class Middleware{constructor(handler,priority=0,id=""){if(typeof handler!=="function")throw new TypeError("Middleware handler must be a function");init(this,"Middleware",{handler});this.handler=handler;this.priority=Number(priority)||0;this.id=String(id||"")}run(event){return this.handler(event)}}
class Timezone{constructor(name="UTC"){const known=/^(UTC|GMT|EET|CET|[A-Za-z]+(?:\/[A-Za-z_+-]+)+)$/;this.name=known.test(String(name||""))?String(name):"UTC";init(this,"Timezone",{name:this.name})}string(){return this.name}toString(){return this.name}}
class FormData{constructor(){init(this,"FormData",{entries:[]})}append(name,value,filename){STATE.get(this).entries.push({name:String(name),value,...(filename===undefined?{}:{filename:String(filename)})})}set(name,value,filename){const s=STATE.get(this);s.entries=s.entries.filter((v)=>v.name!==String(name));this.append(name,value,filename)}get(name){const v=STATE.get(this).entries.find((v)=>v.name===String(name));return v&&v.value}getAll(name){return STATE.get(this).entries.filter((v)=>v.name===String(name)).map((v)=>v.value)}has(name){return STATE.get(this).entries.some((v)=>v.name===String(name))}delete(name){const s=STATE.get(this);s.entries=s.entries.filter((v)=>v.name!==String(name))}export(){return {entries:STATE.get(this).entries.map((v)=>({...v}))}}}
class MailerMessage{constructor(value={}){init(this,"MailerMessage",{...value});Object.assign(this,value)}export(){return {...this}}};class RequestInfo{constructor(value={}){init(this,"RequestInfo",{...value});Object.assign(this,value)}export(){return {...this}}};class RequestEvent{constructor(value={}){init(this,"RequestEvent",{...value});Object.assign(this,value)}export(){return {...this}}};
globalThis.Record=Record;globalThis.Collection=Collection;globalThis.FieldsList=FieldsList;globalThis.Field=Field;globalThis.Command=Command;globalThis.Middleware=Middleware;globalThis.Timezone=Timezone;globalThis.FormData=FormData;globalThis.MailerMessage=MailerMessage;globalThis.RequestInfo=RequestInfo;globalThis.RequestEvent=RequestEvent;
const ident=(v)=>{const q=String.fromCharCode(96);return q+String(v).split(q).join(q+q)+q}, placeholders=(n,start=0)=>Array.from({length:n},(_,i)=>"{:p"+(start+i)+"}").join(", ");
class SQLExpression{constructor(op,args){this.op=op;this.args=args;this.matchFlags=[];Object.defineProperty(this,BRAND,{value:"SQLExpression"})}match(...flags){if(!/like$/i.test(this.op))throw new TypeError("match is only available for LIKE expressions");this.matchFlags=flags.map(Boolean);return this}build(){const a=this.args,b=(x)=>x&&typeof x.build==="function"?x.build():String(x);switch(this.op){case"exp":return String(a[0]);case"hashExp":return Object.keys(a[0]||{}).sort().map((k,i)=>a[0][k]===null?ident(k)+" IS NULL":Array.isArray(a[0][k])?ident(k)+" IN ("+placeholders(a[0][k].length,i)+")":ident(k)+"={:p"+i+"}").join(" AND ");case"not":return"NOT ("+b(a[0])+")";case"and":return a.map((v)=>"("+b(v)+")").join(" AND ");case"or":return a.map((v)=>"("+b(v)+")").join(" OR ");case"in":return ident(a[0])+" IN ("+placeholders(a.length-1)+")";case"notIn":return ident(a[0])+" NOT IN ("+placeholders(a.length-1)+")";case"like":case"orLike":case"notLike":case"orNotLike":{const neg=this.op.includes("not")||this.op.includes("Not"),join=this.op.startsWith("or")?" OR ":" AND ";return a.slice(1).map((_,i)=>ident(a[0])+(neg?" NOT LIKE ":" LIKE ")+"{:p"+i+"}").join(join)}case"exists":return"EXISTS ("+b(a[0])+")";case"notExists":return"NOT EXISTS ("+b(a[0])+")";case"between":return ident(a[0])+" BETWEEN {:p0} AND {:p1}";case"notBetween":return ident(a[0])+" NOT BETWEEN {:p0} AND {:p1}";default:throw new TypeError("unknown SQL expression")}}toJSON(){return {op:this.op,args:this.args,...(this.matchFlags.length?{match:this.matchFlags}:{})}}}
const exp=(op,args)=>new SQLExpression(op,args);
globalThis.$dbx={exp:(sql,params)=>exp("exp",[sql,params||{}]),hashExp:(v)=>exp("hashExp",[v]),not:(v)=>exp("not",[v]),and:(...v)=>exp("and",v),or:(...v)=>exp("or",v),in:(a,...v)=>exp("in",[a,...v]),notIn:(a,...v)=>exp("notIn",[a,...v]),like:(a,...v)=>exp("like",[a,...v]),orLike:(a,...v)=>exp("orLike",[a,...v]),notLike:(a,...v)=>exp("notLike",[a,...v]),orNotLike:(a,...v)=>exp("orNotLike",[a,...v]),exists:(v)=>exp("exists",[v]),notExists:(v)=>exp("notExists",[v]),between:(a,b,c)=>exp("between",[a,b,c]),notBetween:(a,b,c)=>exp("notBetween",[a,b,c])};
globalThis.$http={send:(v)=>H.http.send(v)};
const b64bytes=(text)=>{const chars="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/",clean=String(text||"").replace(/=+$/,"");let bits=0,value=0,out=[];for(const ch of clean){const n=chars.indexOf(ch);if(n<0)throw new TypeError("invalid base64 bytes");value=(value<<6)|n;bits+=6;if(bits>=8){bits-=8;out.push((value>>bits)&255)}}return new Uint8Array(out)};
class FileInfo{constructor(v){Object.assign(this,v)}name(){return this.nameValue}size(){return this.sizeValue}mode(){return this.modeValue}modTime(){return new Date(this.modTimeValue)}isDir(){return this.directory}sys(){return null}}
class DirEntry{constructor(v,path){this.value=v;this.path=path}name(){return this.value.name}isDir(){return this.value.isDir}type(){return this.value.type}info(){return fileInfo(H.os.stat({path:$filepath.join(this.path,this.value.name)}))}}
const fileInfo=(v)=>new FileInfo({nameValue:v.name,sizeValue:v.size,modeValue:v.mode,modTimeValue:v.modTime,directory:v.isDir});
class OSCommand extends Command{constructor(name,args=[]){super();this.name=String(name);this.args=args.map(String);this.input="";this.env=[];this.dir=""}run(){const r=H.os.cmd({name:this.name,args:this.args,input:this.input,env:this.env,dir:this.dir,mode:"run"});if(!r.success)throw new Error(r.error);return r}output(){const r=H.os.cmd({name:this.name,args:this.args,input:this.input,env:this.env,dir:this.dir,mode:"output"});if(!r.success)throw new Error(r.error);return b64bytes(r.stdout)}combinedOutput(){const r=H.os.cmd({name:this.name,args:this.args,input:this.input,env:this.env,dir:this.dir,mode:"combined"});if(!r.success)throw new Error(r.error);return b64bytes(r.output)}}
const rootFS=(root)=>{let closed=false,p=(name)=>{if(closed)throw new Error("root filesystem is closed");return $filepath.join(root,String(name||""))},fs={readFile:(name)=>$os.readFile(p(name)),writeFile:(name,data,mode)=>$os.writeFile(p(name),data,mode),readDir:(name=".")=>$os.readDir(p(name)),stat:(name)=>$os.stat(p(name)),mkdir:(name,mode)=>$os.mkdir(p(name),mode),mkdirAll:(name,mode)=>$os.mkdirAll(p(name),mode),remove:(name)=>$os.remove(p(name)),removeAll:(name)=>$os.removeAll(p(name))};return {fs:()=>fs,close:()=>{closed=true},root}};
globalThis.$os={get args(){return H.os.args({})},getenv:(name)=>H.os.getenv({name}),exit:(code=0)=>H.os.exit({code}),dirFS:(root)=>rootFS(root).fs(),tempDir:()=>H.os.tempDir({}),exec:(name,...args)=>new OSCommand(name,args).run(),cmd:(name,...args)=>new OSCommand(name,args),stat:(path)=>fileInfo(H.os.stat({path})),readFile:(path)=>b64bytes(H.os.readFile({path})),writeFile:(path,data,mode)=>H.os.writeFile({path,data:Array.from(data),mode}),readDir:(path)=>H.os.readDir({path}).map(v=>new DirEntry(v,path)),truncate:(path,size)=>H.os.truncate({path,size}),getwd:()=>H.os.getwd({}),mkdir:(path,mode)=>H.os.mkdir({path,mode}),mkdirAll:(path,mode)=>H.os.mkdirAll({path,mode}),rename:(oldPath,newPath)=>H.os.rename({oldPath,newPath}),remove:(path)=>H.os.remove({path}),removeAll:(path)=>H.os.removeAll({path}),openRoot:(path)=>rootFS(path),openInRoot:(root,path)=>$os.readFile($filepath.join(root,path))};
globalThis.$filesystem={s3:unsupported("$filesystem.s3"),local:(root)=>H.filesystem.local({root}),fileFromPath:(path)=>H.filesystem.fileFromPath({path}),fileFromBytes:(bytes,name)=>H.filesystem.fileFromBytes({bytes,name}),fileFromMultipart:(field)=>H.filesystem.fileFromMultipart({field}),fileFromURL:(url,timeout)=>H.filesystem.fileFromURL({url,timeout}),list:(prefix="",cursor="",limit=100)=>H.filesystem.list({prefix,cursor,limit}),copy:(sourceKey,destinationKey)=>H.filesystem.copy({sourceKey,destinationKey}),read:(key)=>H.filesystem.read({key}),write:(key,data)=>H.filesystem.write({key,data}),delete:(key)=>H.filesystem.delete({key})};
const authMailRecord=(record)=>{const value=record&&typeof record.export==="function"?record.export():record||{};const collection=record&&record.collectionName||value.collection||record&&record.collection||"";const recordId=record&&record.id||value.id||"";if(typeof collection!=="string"||typeof recordId!=="string"||!collection||!recordId)throw new TypeError("a Record is required");return {collection,recordId}};
const authMail=(kind,record,extra={})=>H.authMail.send({kind,...authMailRecord(record),...extra});
globalThis.$mails={sendRecordPasswordReset:($app,record)=>authMail("passwordReset",record),sendRecordVerification:($app,record)=>authMail("verification",record),sendRecordChangeEmail:($app,record,newEmail)=>authMail("changeEmail",record,{newEmail}),sendRecordOTP:($app,record,otpId,otpPass)=>authMail("otp",record,{otpId,otpPass}),sendRecordAuthAlert:($app,record,alertInfo)=>authMail("authAlert",record,{alertInfo})};
class ApiError extends Error{constructor(status=500,message="",data={}){super(message);this.status=status;this.data=data}}; globalThis.ApiError=ApiError; globalThis.NotFoundError=class extends ApiError{constructor(m,d){super(404,m,d)}};globalThis.BadRequestError=class extends ApiError{constructor(m,d){super(400,m,d)}};globalThis.ForbiddenError=class extends ApiError{constructor(m,d){super(403,m,d)}};globalThis.UnauthorizedError=class extends ApiError{constructor(m,d){super(401,m,d)}};globalThis.TooManyRequestsError=class extends ApiError{constructor(m,d){super(429,m,d)}};globalThis.InternalServerError=class extends ApiError{constructor(m,d){super(500,m,d)}};
const principal=(e)=>e&&(e.principal||(e.requestInfo&&e.requestInfo.auth));
const middleware=(check,code)=>(next)=>(e)=>{if(!check(e))throw new ApiError(code,code===401?"authentication required":"forbidden");return next(e)};
const routeResponse=(response)=>response&&typeof response==="object"?response:{status:204};
const staticRoot=(root)=>typeof root==="string"?root:(root&&typeof root.root==="string"?root.root:"");
const bodyLimit=(limit,next)=>{if(!Number.isSafeInteger(limit)||limit<1)throw new ApiError(400,"invalid body limit");const wrap=(handler)=>{if(typeof handler!=="function")throw new ApiError(500,"body limit handler is required");const wrapped=(e)=>handler(e);wrapped.pgbaseBodyLimit=limit;return wrapped};return next===undefined?wrap:wrap(next)};
globalThis.$apis={static:(root,indexFallback=false)=>(e)=>({status:200,headers:{},transport:{static:{root:staticRoot(root),path:(e&&e.pathValues&&(e.pathValues.path||e.pathValues.$))||"",indexFallback:!!indexFallback}}}),requireGuestOnly:middleware((e)=>!principal(e),403),requireAuth:(...collections)=>middleware((e)=>{const p=principal(e);return !!p&&(!collections.length||collections.includes(p.collection||p.kind))},401),requireSuperuserAuth:middleware((e)=>{const p=principal(e);return !!p&&(p.kind==="admin"||p.superuser===true)},401),requireSuperuserOrOwnerAuth:(ownerIdPathParam="id")=>middleware((e)=>{const p=principal(e),owner=e&&e.pathValues&&e.pathValues[ownerIdPathParam];return !!p&&(p.kind==="admin"||p.superuser===true||p.id===owner)},403),skipSuccessActivityLog:(next)=>{next.pgbaseSkipSuccessActivityLog=true;return next},gzip:(next)=>(e)=>{const response=routeResponse(next(e));response.transport=Object.assign({},response.transport,{gzip:true});return response},bodyLimit,recordAuthResponse:(e,record,method,meta)=>{e.response={record,authMethod:method,meta};return e.response},enrichRecord:(record)=>record,enrichRecords:(records)=>records};
const recordRef=(record,expand="")=>{const s=record&&STATE.get(record);return {collection:s?s.collection:(record&&record.collectionName||record&&record.collection||""),recordId:s?s.id:String(record&&record.id||""),expand}};
const mergeRecord=(record,value)=>{const data=value&&value.data||value||{};if(record&&STATE.get(record)){const s=STATE.get(record);s.data={...data};s.id=String(data.id||s.id||"");return record}return data};
Object.assign(globalThis.$apis,{recordAuthResponse:(e,record,method,meta)=>H.apis.recordAuthResponse({...recordRef(record),authMethod:method,meta:meta||null}),enrichRecord:(e,record,...expands)=>mergeRecord(record,H.apis.enrichRecord(recordRef(record,expands.join(",")))),enrichRecords:(e,records,...expands)=>{const values=H.apis.enrichRecords((records||[]).map(r=>recordRef(r,expands.join(","))));return (records||[]).map((r,i)=>mergeRecord(r,values[i]))}});
const recordInput=(record)=>{if(record&&STATE.get(record)){const value=record.export();return {collection:value.collection,id:value.id,input:value.data}};return {collection:record.collection||record.collectionName,id:record.id,input:record.input||record.data||record}};
const appStore=Object.freeze({get:(key)=>H["app-control"].storeGet({key}),set:(key,value)=>H["app-control"].storeSet({key,value}),delete:(key)=>H["app-control"].storeDelete({key})});
const appCron=Object.freeze({list:()=>H["app-control"].cronList({}),configure:(id,schedule,timezone="UTC",enabled=true)=>H["app-control"].cronConfigure({id,schedule,timezone,enabled}),run:(id)=>H["app-control"].cronRun({id})});
const backupFS=Object.freeze({list:()=>H["app-control"].backupList({}),read:(id)=>H["app-control"].backupRead({id}),delete:(id)=>H["app-control"].backupDelete({id})});
const broker=Object.freeze({clients:()=>H["app-control"].brokerClients({}),publish:(topic,data)=>H["app-control"].brokerPublish({topic,data})});
const appRecord=(method,value)=>H.app[method](value);
const appCatalog=(method,value)=>H.app[method](value);
const appDB=Object.freeze({transaction:(statements)=>H["app-control"].databaseTransaction({statements}),tables:()=>H["app-control"].databaseTables({}),columns:(name)=>H["app-control"].databaseColumns({name}),indexes:(name)=>H["app-control"].databaseIndexes({name})});
const runTx=(callback)=>{const statements=[],tx=Object.freeze({exec:(sql,args=[])=>statements.push({sql,args}),query:(sql,args=[])=>statements.push({sql,args,query:true})});callback(tx);return H["app-control"].databaseTransaction({statements})};
const collectionEnvelope=(value)=>({collection:value instanceof Collection?value.export():value});
globalThis.$app={isBootstrapped:()=>H["app-control"].isBootstrapped({}),bootstrap:unsupported("$app.bootstrap"),resetBootstrapState:unsupported("$app.resetBootstrapState"),dataDir:()=>H["app-control"].dataDir({}),encryptionEnv:()=>H["app-control"].encryptionEnv({}),isDev:()=>H["app-control"].isDev({}),settings:()=>H["app-control"].settings({}),store:()=>appStore,cron:()=>appCron,subscriptionsBroker:unsupported("$app.subscriptionsBroker"),newBackupsFilesystem:()=>backupFS,reloadSettings:()=>H["app-control"].reloadSettings({}),createBackup:()=>H["app-control"].createBackup({}),restoreBackup:(id)=>H["app-control"].restoreBackup({id}),restart:()=>H["app-control"].restart({}),runSystemMigrations:()=>H["app-control"].runSystemMigrations({}),runAppMigrations:()=>H["app-control"].runAppMigrations({}),runAllMigrations:()=>H["app-control"].runAllMigrations({}),isTransactional:()=>false,transactionInfo:()=>H["app-control"].transactionInfo({}),unsafeWithoutHooks:unsupported("$app.unsafeWithoutHooks"),recordQuery:(collection,filter,sort,limit)=>appRecord("record.query",{collection,filter,sort,limit}),findRecordsByFilter:(collection,filter,sort,limit)=>require("pgbase").records.list({collection,filter,sort,limit}),findFirstRecordByFilter:(collection,filter)=>require("pgbase").records.list({collection,filter,limit:1}).items[0],findRecordById:(collection,id)=>require("pgbase").records.get({collection,id}),save:(value)=>value instanceof Collection?H["app-control"].collectionSave(collectionEnvelope(value)):value.id?require("pgbase").records.update(recordInput(value)):require("pgbase").records.create(recordInput(value)),saveNoValidate:(value)=>value instanceof Collection?(H["app-control"].schemaExport({}),H["hooks-bypass"].collectionSaveNoValidate(collectionEnvelope(value))):$app.save(value),validate:(value)=>value instanceof Collection?H["app-control"].collectionValidate(collectionEnvelope(value)):{valid:true},delete:(value)=>value instanceof Collection?H["app-control"].collectionDelete(collectionEnvelope(value)):require("pgbase").records.delete({collection:value.collection||value.collectionName,id:value.id}),reloadCachedCollections:()=>H["app-control"].schemaExport({}),importCollections:(collections)=>H["app-control"].schemaPreview({document:{format:"pgbase.schema/v1",collections}}),syncRecordTableSchema:(collection)=>H["app-control"].collectionSync(collectionEnvelope(collection)),truncateCollection:(name)=>H["app-control"].collectionTruncate({name:typeof name==="string"?name:name.name}),db:()=>appDB,concurrentDB:()=>appDB,nonconcurrentDB:()=>appDB,auxDB:()=>appDB,auxConcurrentDB:()=>appDB,auxNonconcurrentDB:()=>appDB,runInTransaction:runTx,auxRunInTransaction:runTx,logger:()=>require("pgbase").log,mailer:()=>require("pgbase").mail,filesystem:()=>$filesystem};
Object.assign(globalThis.$app,{subscriptionsBroker:()=>broker,unsafeWithoutHooks:(fn)=>{if(typeof fn!=="function")throw new TypeError("callback required");H["hooks-bypass"].begin({});try{return fn()}finally{H["hooks-bypass"].end({})}},findRecordsByIds:(collection,ids)=>appRecord("record.findByIds",{collection,ids}).items.map(v=>new Record(collection,v.data||v)),findAllRecords:(collection)=>appRecord("record.findAll",{collection}).items.map(v=>new Record(collection,v.data||v)),findFirstRecordByData:(collection,field,value)=>new Record(collection,appRecord("record.findFirstByData",{collection,field,value})),countRecords:(collection,filter)=>appRecord("record.count",{collection,filter}),canAccessRecord:(record,requestInfo,rule)=>appRecord("record.canAccess",{collection:record.collectionName,id:record.id,rule}),expandRecord:(record,expand)=>new Record(record.collectionName,appRecord("record.expand",{collection:record.collectionName,id:record.id,expand}).data),expandRecords:(records,expand)=>appRecord("record.expandMany",{collection:records[0]&&records[0].collectionName,ids:records.map(r=>r.id),expand}).items.map(v=>new Record(records[0].collectionName,v)),logQuery:(filter)=>appRecord("log.query",filter||{}),findLogById:(id)=>appRecord("log.find",{id}),logsStats:()=>appRecord("log.stats",{}),deleteOldLogs:(before)=>appRecord("log.deleteOld",{to:before})});
Object.assign(globalThis.$app,{findCollectionByNameOrId:(name)=>new Collection(appCatalog("collection.get",{name})),findCachedCollectionByNameOrId:(name)=>new Collection(appCatalog("collection.cachedGet",{name})),findAllCollections:(type)=>appCatalog("collection.list",{type}).items.map(v=>new Collection(appCatalog("collection.get",{name:v.id}))),findCollectionReferences:(collection)=>appCatalog("collection.references",{name:typeof collection==="string"?collection:collection.id||collection.name}),findCachedCollectionReferences:(collection)=>appCatalog("collection.references",{name:typeof collection==="string"?collection:collection.id||collection.name}),isCollectionNameUnique:(name,excludeId="")=>appCatalog("collection.nameUnique",{name,excludeId}),findAuthAuxiliary:(collection,recordId,kind)=>appCatalog("authAux.list",{collection,recordId,kind}).items,deleteAuthAuxiliary:(collection,recordId,kind,id)=>appCatalog("authAux.delete",{collection,recordId,kind,id})});
})();`
