//go:build integration

package pgbase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// TestACMEHTTP01IssuesCachesAndServesCertificate exercises autocert against a
// local RFC8555 subset. The mock validates its own HTTP-01 request against the
// app listener; no public CA, domain registration, or external network exists.
func TestACMEHTTP01IssuesCachesAndServesCertificate(t *testing.T) {
	ca := newMockACME(t)
	cache := t.TempDir()
	httpsAddress, challengeAddress := unusedAddress(t), unusedAddress(t)
	ca.challengeAddress = challengeAddress
	options := func() Options {
		return Options{DatabaseURL: testutil.NewDatabase(t), HTTPAddress: unusedAddress(t), TokenSecret: make([]byte, 32), AutoMigrate: true, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), TLS: &TLSOptions{Address: httpsAddress, ACMEHosts: []string{"acme.test"}, ACMECacheDirectory: cache, HTTPChallengeAddress: challengeAddress, ACMEDirectoryURL: ca.server.URL}}
	}
	first := runACMEApp(t, options())
	redirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: time.Second}
	request, err := http.NewRequest(http.MethodGet, "http://"+challengeAddress+"/ordinary", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "acme.test"
	response, err := redirect.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "https://acme.test/ordinary" {
		t.Fatalf("HTTP redirect status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()
	if err := dialTLS(httpsAddress, "unknown.test", ca.roots); err == nil {
		t.Fatal("unknown ACME host completed TLS")
	}
	if ca.orders() != 0 {
		t.Fatalf("unknown host requested ACME order count=%d", ca.orders())
	}
	if err := dialTLS(httpsAddress, "acme.test", ca.roots); err != nil {
		t.Fatalf("issued ACME TLS handshake: %v", err)
	}
	if ca.orders() != 1 || ca.challengeHits() != 1 {
		t.Fatalf("orders=%d HTTP-01 hits=%d", ca.orders(), ca.challengeHits())
	}
	deadline := time.Now().Add(10 * time.Second)
	for ca.orders() != 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ca.orders() != 2 {
		t.Fatalf("short-lived ACME certificate did not renew; orders=%d", ca.orders())
	}
	first.stop(t)

	second := runACMEApp(t, options())
	if err := dialTLS(httpsAddress, "acme.test", ca.roots); err != nil {
		t.Fatalf("cached ACME TLS handshake: %v", err)
	}
	if ca.orders() != 2 {
		t.Fatalf("cache reuse requested another ACME certificate: orders=%d", ca.orders())
	}
	second.stop(t)
	if connection, err := net.DialTimeout("tcp", challengeAddress, 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("ACME HTTP challenge listener remained open after shutdown")
	}
}

type acmeRun struct {
	cancel context.CancelFunc
	done   <-chan error
}

func runACMEApp(t *testing.T, options Options) acmeRun {
	t.Helper()
	app, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if connection, err := net.DialTimeout("tcp", options.TLS.HTTPChallengeAddress, 20*time.Millisecond); err == nil {
			_ = connection.Close()
			return acmeRun{cancel, done}
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = <-done
	t.Fatal("ACME challenge listener did not start")
	return acmeRun{}
}
func (r acmeRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("ACME app shutdown: %v", err)
	}
}
func dialTLS(address, name string, roots *x509.CertPool) error {
	connection, err := tls.Dial("tcp", address, &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err == nil {
		return connection.Close()
	}
	return err
}

type mockACME struct {
	t                    *testing.T
	server               *httptest.Server
	rootKey              *ecdsa.PrivateKey
	root                 *x509.Certificate
	roots                *x509.CertPool
	mu                   sync.Mutex
	orderCount, hitCount int
	authorized, issued   bool
	challengeAddress     string
	certificate          []byte
}

func newMockACME(t *testing.T) *mockACME {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "PgBase test ACME root"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ca := &mockACME{t: t, rootKey: key, root: root, roots: x509.NewCertPool()}
	ca.roots.AddCert(root)
	ca.server = httptest.NewServer(http.HandlerFunc(ca.serve))
	t.Cleanup(ca.server.Close)
	return ca
}
func (ca *mockACME) orders() int            { ca.mu.Lock(); defer ca.mu.Unlock(); return ca.orderCount }
func (ca *mockACME) challengeHits() int     { ca.mu.Lock(); defer ca.mu.Unlock(); return ca.hitCount }
func (ca *mockACME) url(path string) string { return ca.server.URL + path }
func (ca *mockACME) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Replay-Nonce", "fixture-nonce")
	switch r.URL.Path {
	case "/":
		json.NewEncoder(w).Encode(map[string]string{"newNonce": ca.url("/nonce"), "newAccount": ca.url("/account"), "newOrder": ca.url("/order")})
	case "/nonce":
	case "/account":
		w.Header().Set("Location", ca.url("/account/1"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("{}"))
	case "/order":
		ca.mu.Lock()
		ca.orderCount++
		ca.mu.Unlock()
		w.Header().Set("Location", ca.url("/orders/1"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"status": "pending", "authorizations": []string{ca.url("/authz/1")}})
	case "/authz/1":
		ca.mu.Lock()
		valid := ca.authorized
		ca.mu.Unlock()
		status := "pending"
		if valid {
			status = "valid"
		}
		json.NewEncoder(w).Encode(map[string]any{"status": status, "challenges": []map[string]string{{"type": "http-01", "url": ca.url("/challenge/1"), "token": "fixture-token"}}})
	case "/challenge/1":
		ca.mu.Lock()
		address := ca.challengeAddress
		ca.mu.Unlock()
		request, requestErr := http.NewRequest(http.MethodGet, "http://"+address+"/.well-known/acme-challenge/fixture-token", nil)
		if requestErr != nil {
			http.Error(w, requestErr.Error(), http.StatusInternalServerError)
			return
		}
		request.Host = "acme.test"
		response, err := (&http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}).Do(request)
		if err != nil || response.StatusCode != http.StatusOK {
			ca.t.Logf("HTTP-01 response address=%s status=%v err=%v", address, func() any {
				if response == nil {
					return nil
				}
				return response.StatusCode
			}(), err)
			http.Error(w, "HTTP-01 validation failed", http.StatusBadRequest)
			if response != nil {
				_ = response.Body.Close()
			}
			return
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if len(body) == 0 {
			http.Error(w, "empty HTTP-01 response", http.StatusBadRequest)
			return
		}
		ca.mu.Lock()
		ca.authorized = true
		ca.hitCount++
		ca.mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	case "/orders/1":
		ca.mu.Lock()
		ready := ca.authorized
		issued := ca.issued
		ca.mu.Unlock()
		status := "pending"
		result := map[string]any{"status": status, "authorizations": []string{ca.url("/authz/1")}}
		if ready {
			result["status"] = "ready"
			result["finalize"] = ca.url("/finalize/1")
		}
		if issued {
			result["status"] = "valid"
			result["certificate"] = ca.url("/cert/1")
		}
		json.NewEncoder(w).Encode(result)
	case "/finalize/1":
		csr, err := acmePayloadCSR(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		certificate, err := ca.issue(csr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ca.mu.Lock()
		ca.issued = true
		ca.certificate = certificate
		ca.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"status": "valid", "certificate": ca.url("/cert/1")})
	case "/cert/1":
		ca.mu.Lock()
		certificate := append([]byte(nil), ca.certificate...)
		ca.mu.Unlock()
		if len(certificate) == 0 {
			http.Error(w, "certificate not issued", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: certificate})
		_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: ca.root.Raw})
	default:
		http.Error(w, "unknown ACME endpoint", http.StatusNotFound)
	}
}
func acmePayloadCSR(r *http.Request) (*x509.CertificateRequest, error) {
	var envelope struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, err
	}
	var request struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	der, err := base64.RawURLEncoding.DecodeString(request.CSR)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificateRequest(der)
}
func (ca *mockACME) issue(csr *x509.CertificateRequest) ([]byte, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "acme.test"}, DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Second), NotAfter: time.Now().Add(6 * time.Second), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	return x509.CreateCertificate(rand.Reader, leaf, ca.root, csr.PublicKey, ca.rootKey)
}
