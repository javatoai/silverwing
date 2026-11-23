package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testCertificate returns a self-signed certificate named name with
// organizational unit team (none when ""), in PEM, and its SHA-1 hash as
// find-identity prints it.
func testCertificate(t *testing.T, name, team string) (pemData []byte, hash string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	subject := pkix.Name{CommonName: name}
	if team != "" {
		subject.OrganizationalUnit = []string{team}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      subject,
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(0, 0).AddDate(100, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), strings.ToUpper(hex.EncodeToString(sum[:]))
}

// testKeychain returns the output of security find-identity -v -p
// codesigning and of security find-certificate -a -p for a keychain with an
// identity of each name, of the team its name maps to.
func testKeychain(t *testing.T, names []string, teams map[string]string) (identities string, certs []byte, hashes map[string]string) {
	t.Helper()
	hashes = map[string]string{}
	for i, name := range names {
		p, hash := testCertificate(t, name, teams[name])
		identities += fmt.Sprintf("  %d) %s \"%s\"\n", i+1, hash, name)
		certs = append(certs, p...)
		hashes[name] = hash
	}
	identities += fmt.Sprintf("     %d valid identities found\n", len(names))
	return identities, certs, hashes
}

const (
	otherDev = "Apple Development: John Roe (ZYXWV98765)"
	teamDev  = "Apple Development: Jane Doe (ABCDE12345)"
)

// devSign reads the keychain with the security command, which this test
// replaces on PATH.
func TestDevSignTeam(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("mygo dev signs with a development identity on macOS only")
	}
	identities, certs, hashes := testKeychain(t, []string{otherDev, teamDev}, map[string]string{otherDev: "OTHER67890", teamDev: "TEAM123456"})
	dir := t.TempDir()
	for name, data := range map[string][]byte{"identities": []byte(identities), "certs": certs} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\ncase $1 in\nfind-identity) cat '" + filepath.Join(dir, "identities") + "' ;;\nfind-certificate) cat '" + filepath.Join(dir, "certs") + "' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := &Config{}
	c.MacOS.SigningIdentity = "Developer ID Application: Example (TEAM123456)"
	if got := devSign(c, ""); got != hashes[teamDev] {
		t.Errorf("devSign = %s, want %s (%s, of team TEAM123456)", got, hashes[teamDev], teamDev)
	}
}
