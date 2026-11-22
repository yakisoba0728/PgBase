package backup

import "testing"

func TestPlaintextEndpointWarnsOnlyForHTTPToNonLocalHosts(t *testing.T) {
	for _, testCase := range []struct {
		endpoint string
		want     bool
	}{
		{"", false},
		{"https://s3.example.com", false},
		{"HTTPS://s3.example.com", false},
		{"https://minio:9000", false},
		{"http://localhost:9000", false},
		{"http://LOCALHOST.:9000", false},
		{"http://minio.localhost:9000", false},
		{"http://127.0.0.1:9000", false},
		{"http://127.8.9.10", false},
		{"http://[::1]:9000", false},
		{"http://[::ffff:127.0.0.1]:9000", false},
		{"http://10.1.2.3:9000", false},
		{"http://172.16.0.9", false},
		{"http://192.168.1.20:9000", false},
		{"http://169.254.1.1", false},
		{"http://[fd00::1]:9000", false},
		{"http://[fe80::1]:9000", false},
		// Plain http to anything a name or a public address can reach is
		// reported: a hostname alone does not prove the traffic stays local.
		{"http://minio:9000", true},
		{"http://minio.internal:9000", true},
		{"http://s3.example.com", true},
		{"HTTP://s3.example.com", true},
		{"http://203.0.113.10:9000", true},
		{"http://172.32.0.1", true},
		{"http://[2001:db8::1]:9000", true},
		{"http://0.0.0.0:9000", true},
	} {
		if got := PlaintextEndpoint(testCase.endpoint); got != testCase.want {
			t.Errorf("PlaintextEndpoint(%q)=%v, want %v", testCase.endpoint, got, testCase.want)
		}
	}
}
