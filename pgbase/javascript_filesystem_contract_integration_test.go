//go:build integration

package pgbase

import (
	"net/http"
	"testing"
)

func TestJavaScriptFilesystemRejectsStorageConfiguration(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/filesystem-contract","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`, `exports.run=()=>{
		let code="accepted";
		try {$filesystem.s3("other-bucket","other-region","https://other.test","access","secret",true).upload("YQ==","unexpected");}
		catch (e) {code=e.code;}
		const page=$filesystem.list();
		const objects=(page.Objects||page.objects||[]).filter(v=>v&&typeof v==="object").length;
		$filesystem.configured().upload("Yg==","native");
		return {status:200,json:{code,objects,alias:$filesystem.s3().exists("native")}};
	}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}}
	})
	response := publicUsersRequest(t, fixture, http.MethodPost, "/filesystem-contract", map[string]any{}, "", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"alias":true,"code":"filesystem_configuration_unsupported","objects":0}` {
		t.Fatalf("storage selection response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestJavaScriptFilesystemCannotReplaceInvocationContext(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/filesystem-context","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`, `exports.run=()=>{
		const codes=[];
		for(const fs of [$filesystem.configured(),$filesystem.local(".")]){
			try {fs.setContext({deadline:0});codes.push("accepted");}catch(e){codes.push(e.code);}
		}
		return {status:200,json:{codes}};
	}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
		options.JavaScript = &JavaScriptOptions{
			Directory: directory, Capabilities: map[string]bool{"filesystem": true},
			PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: t.TempDir()},
		}
	})
	response := publicUsersRequest(t, fixture, http.MethodPost, "/filesystem-context", map[string]any{}, "", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"codes":["filesystem_context_unsupported","filesystem_context_unsupported"]}` {
		t.Fatalf("context replacement response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestJavaScriptFilesystemListingRequiresContinuation(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/filesystem-pages","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`, `exports.run=()=>{
		const fs=$filesystem.configured();
		for(let i=0;i<101;i++)fs.upload("YQ==","items/"+String(i).padStart(3,"0"));
		fs.upload("Yg==","outside");
		let code="accepted";
		try {fs.list("items/");} catch(e) {code=e.code;}
		if(code!=="filesystem_list_requires_pagination")return {status:200,json:{code}};
		const first=fs.listPage("items/","",100),last=fs.listPage("items/",first.nextCursor,100);
		const invalid=[];
		for(const limit of [0,1001,1.5]){
			try {fs.listPage("items/","",limit);invalid.push("accepted");}catch(e){invalid.push(e.code);}
		}
		return {status:200,json:{code,first:first.objects.length,last:last.objects.length,
			firstKey:first.objects[0].Key,lastKey:last.objects[0].Key,cursor:first.nextCursor,
			done:last.nextCursor==="",small:fs.list("outside").length,invalid}};
	}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}}
	})
	response := publicUsersRequest(t, fixture, http.MethodPost, "/filesystem-pages", map[string]any{}, "", nil, nil)
	want := `{"code":"filesystem_list_requires_pagination","cursor":"items/099","done":true,"first":100,"firstKey":"items/000","invalid":["filesystem_invalid_page_limit","filesystem_invalid_page_limit","filesystem_invalid_page_limit"],"last":1,"lastKey":"items/100","small":1}`
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("listing response=%d body=%s", response.Code, response.Body.String())
	}
}
