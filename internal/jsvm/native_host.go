package jsvm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dop251/goja"
)

// Native services exchange bounded JSON directly with the admitted host. They
// do not construct compatibility models or install process-wide globals.
func (r *Runtime) installNativeHostServices(vm *goja.Runtime, state *callState, exports *goja.Object) error {
	for _, service := range []struct {
		name, capability string
		methods          map[string]string
	}{
		{"http", "http", map[string]string{"send": "http.send"}},
		{"os", "os", map[string]string{
			"args": "os.args", "getenv": "os.getenv", "command": "os.cmd", "exit": "os.exit",
		}},
		{"filesystem", "filesystem", map[string]string{
			"read": "host.filesystem.read", "write": "host.filesystem.write", "stat": "os.stat", "list": "os.readDir",
			"mkdir": "os.mkdir", "rename": "os.rename", "remove": "os.remove",
		}},
		{"jobs", "app-control", map[string]string{
			"list": "app-control.cronList", "configure": "app-control.cronConfigure", "run": "app-control.cronRun",
		}},
		{"realtime", "app-control", map[string]string{
			"clients": "app-control.brokerClients", "publish": "app-control.brokerPublishApplication",
		}},
		{"backups", "app-control", map[string]string{
			"list": "app-control.backupList", "create": "app-control.createBackup", "restore": "app-control.restoreBackup",
			"read": "app-control.backupRead", "delete": "app-control.backupDelete",
		}},
		{"schema", "app-control", map[string]string{
			"export": "app-control.schemaExport", "preview": "app-control.schemaPreview", "apply": "app-control.schemaApply",
		}},
		{"app", "app-control", map[string]string{
			"settings": "app-control.settings", "reloadSettings": "app-control.reloadSettings",
			"restart": "app-control.restart", "transactionInfo": "app-control.transactionInfo",
		}},
	} {
		object := vm.NewObject()
		for name, method := range service.methods {
			if err := object.Set(name, r.hostFunction(vm, state, service.capability, method)); err != nil {
				return err
			}
		}
		if service.name == "app" {
			if err := object.Set("transaction", func(call goja.FunctionCall) goja.Value {
				return r.nativeTransaction(vm, state, exports, call)
			}); err != nil {
				return err
			}
		}
		if err := exports.Set(service.name, object); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) nativeTransaction(vm *goja.Runtime, state *callState, services *goja.Object, call goja.FunctionCall) goja.Value {
	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(vm.NewTypeError("transaction callback is required"))
	}
	if state.transactionActive {
		result, err := callback(goja.Undefined(), services)
		if err != nil {
			panic(err)
		}
		return synchronousTransactionResult(vm, result)
	}
	begin := r.hostFunction(vm, state, "app-control", "app-control.databaseBegin")
	begun := begin(goja.FunctionCall{Arguments: []goja.Value{vm.ToValue(map[string]any{})}})
	token := begun.ToObject(vm).Get("token").String()
	input := vm.ToValue(map[string]string{"token": token})
	committed := false
	state.transactionActive = true
	defer func() {
		state.transactionActive = false
		if !committed {
			// Cleanup is owned by the runtime even when the script exhausted its
			// call budget or its invocation was canceled.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(state.ctx), time.Second)
			defer cancel()
			raw, _ := json.Marshal(map[string]string{"token": token})
			_, _ = state.host.Call(ctx, "app-control.databaseRollback", raw)
		}
	}()
	result, err := callback(goja.Undefined(), services)
	if err != nil {
		panic(err)
	}
	result = synchronousTransactionResult(vm, result)
	commit := r.hostFunction(vm, state, "app-control", "app-control.databaseCommit")
	commit(goja.FunctionCall{Arguments: []goja.Value{input}})
	committed = true
	return result
}

func synchronousTransactionResult(vm *goja.Runtime, value goja.Value) goja.Value {
	if object, ok := value.(*goja.Object); ok {
		if _, asynchronous := goja.AssertFunction(object.Get("then")); asynchronous {
			panic(vm.NewTypeError("transaction callback must be synchronous"))
		}
	}
	return value
}
