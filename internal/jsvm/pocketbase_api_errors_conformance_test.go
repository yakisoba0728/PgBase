package jsvm_test

import (
	"context"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestPocketBaseAPIErrorConstructorDefaults(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/errors","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{
		const values=[new ApiError(),new NotFoundError(),new BadRequestError(),new ForbiddenError(),new UnauthorizedError(),new TooManyRequestsError(),new InternalServerError()];
		return values.map(e=>({status:e.status,message:e.message,data:e.rawData()}));
	}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	out, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"data":null,"message":"","status":0},{"data":null,"message":"The requested resource wasn't found.","status":404},{"data":null,"message":"Something went wrong while processing your request.","status":400},{"data":null,"message":"You are not allowed to perform this request.","status":403},{"data":null,"message":"Missing or invalid authentication.","status":401},{"data":null,"message":"Too Many Requests.","status":429},{"data":null,"message":"Something went wrong while processing your request.","status":500}]`
	if string(out) != want {
		t.Fatalf("errors=%s, want %s", out, want)
	}
}
