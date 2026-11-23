package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestFileAssociationMimeTypes(t *testing.T) {
	for _, bad := range []string{"plain", "text/", "/plain", "text/plain; charset=utf-8", "text/plain;", "text/plain\nExec=other", `application/x-test"/><broken`, "application/x-test value"} {
		c := &Config{FileAssociations: []FileAssociation{{Ext: []string{"note"}, MimeType: bad}}}
		if err := c.validate(); err == nil || !strings.Contains(err.Error(), "fileAssociations[0].mimeType") {
			t.Errorf("MIME type %q: %v", bad, err)
		}
	}
	for _, value := range []string{"", "text/markdown", "application/vnd.example+json", "application/x-notes&draft"} {
		c := &Config{FileAssociations: []FileAssociation{{Ext: []string{"note"}, MimeType: value}}}
		if err := c.validate(); err != nil {
			t.Errorf("valid MIME type %q: %v", value, err)
		}
	}
}

func TestMimePackageEscapesType(t *testing.T) {
	const name = "application/x-notes&draft"
	c := &Config{FileAssociations: []FileAssociation{{Ext: []string{"note"}, MimeType: name}}}
	var pkg struct {
		Types []struct {
			Name string `xml:"type,attr"`
		} `xml:"mime-type"`
	}
	if err := xml.Unmarshal([]byte(mimePackage(c)), &pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Types) != 1 || pkg.Types[0].Name != name {
		t.Fatalf("MIME package = %+v", pkg)
	}
}

func TestFileAssociationMimeTypeNormalization(t *testing.T) {
	c := &Config{FileAssociations: []FileAssociation{{Ext: []string{"note"}, MimeType: " Application/X-Notes "}}}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if got, defined := c.mimeType(c.FileAssociations[0]); got != "application/x-notes" || !defined {
		t.Fatalf("MIME type = %q, defined=%v", got, defined)
	}
}
