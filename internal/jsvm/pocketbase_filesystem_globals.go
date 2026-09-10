package jsvm

import "github.com/dop251/goja"

// installPocketBaseFilesystemGlobals keeps the capability-scoped filesystem
// façade separate from bootstrap brands and constructors.
func (r *Runtime) installPocketBaseFilesystemGlobals(vm *goja.Runtime, state *callState) error {
	_, err := vm.RunString(`(function(){const H=__pgbaseCompatHost;
const stream=(key)=>{const open=H.filesystem.open({key});return {read:(maximumBytes=65536)=>H.filesystem.readChunk({handle:open.handle,maximumBytes}),close:()=>H.filesystem.close({handle:open.handle})}};
const writer=(key)=>{const begin=H.filesystem.beginWrite({key});return {writeChunk:(data)=>H.filesystem.writeChunk({handle:begin.handle,data}),commit:()=>H.filesystem.commit({handle:begin.handle}),abort:()=>H.filesystem.abort({handle:begin.handle})}};
globalThis.$filesystem={
 s3:()=>Object.freeze({list:(prefix="",cursor="",limit=100)=>H.filesystem.list({prefix,cursor,limit}),copy:(sourceKey,destinationKey)=>H.filesystem.copy({sourceKey,destinationKey}),open:stream}),
 local:(root)=>H.filesystem.local({root}),fileFromPath:(path)=>H.filesystem.fileFromPath({path}),fileFromBytes:(bytes,name)=>H.filesystem.fileFromBytes({bytes,name}),fileFromMultipart:(input)=>H.filesystem.fileFromMultipart(input),fileFromURL:(url,timeout)=>H.filesystem.fileFromURL({url,timeout}),
 list:(prefix="",cursor="",limit=100)=>H.filesystem.list({prefix,cursor,limit}),copy:(sourceKey,destinationKey)=>H.filesystem.copy({sourceKey,destinationKey}),open:stream,beginWrite:writer,read:(key)=>stream(key),write:(key,data)=>{if(data&&typeof data==="object"){if(typeof data.handle==="string")return H.filesystem.commitMultipart({key,handle:data.handle});if(data.kind==="inline"&&typeof data.name==="string"&&typeof data.data==="string"&&Number.isSafeInteger(Number(data.size))&&Number(data.size)>=0)return H.filesystem.write({key,data:data.data,descriptor:{kind:data.kind,name:data.name,size:Number(data.size)}});throw new TypeError("invalid filesystem file descriptor")}return H.filesystem.write({key,data})},delete:(key)=>H.filesystem.delete({key})};
const p=globalThis.$filepath; p.glob=(pattern)=>H.os.glob({path:pattern}); p.walk=(root)=>H.os.walk({path:root});p.walkDir=(root)=>H.os.walkDir({path:root});
const os=globalThis.$os;os.dirFS=(root)=>Object.freeze({root,readFile:(path)=>H.os.readFile({path:root+"/"+path}),readDir:(path="")=>H.os.readDir({path:root+"/"+path}),walk:(path="")=>H.os.walk({path:root+"/"+path}),walkDir:(path="")=>H.os.walkDir({path:root+"/"+path})});})();`)
	return err
}
