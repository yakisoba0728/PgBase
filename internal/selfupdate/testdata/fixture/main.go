package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

var (
	version   = "v0.0.0"
	commit    = "0000000000000000000000000000000000000000"
	goos      = "darwin"
	goarch    = "arm64"
	schemaMin = ""
	schemaMax = ""
	failName  = ""
)

func main() {
	if failName != "" && filepath.Base(os.Args[0]) == failName {
		os.Exit(17)
	}
	if len(os.Args) == 3 && os.Args[1] == "version" && os.Args[2] == "--json" {
		build := map[string]string{"version": version, "commit": commit, "goos": goos, "goarch": goarch}
		if schemaMin != "" && schemaMax != "" {
			build["schemaMin"] = schemaMin
			build["schemaMax"] = schemaMax
		}
		_ = json.NewEncoder(os.Stdout).Encode(build)
		return
	}
	os.Exit(2)
}
