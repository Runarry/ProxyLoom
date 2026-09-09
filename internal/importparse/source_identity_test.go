package importparse

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/origin"
)

func TestExplicitSourceIdentityIsValidatedIsolatedMetadata(t *testing.T) {
	for _, input := range []string{
		"http://source.example.invalid:8080?external_key=item-1#First",
		"http://changed.example.invalid:8081?external_key=item-1#Renamed",
	} {
		candidate := ParseURI(input)
		if !candidate.Valid() || origin.ExternalKeyFromMetadata(candidate.Metadata) != "item-1" {
			t.Fatal("explicit source identity was not retained")
		}
		encoded, _ := json.Marshal(candidate.Node)
		if strings.Contains(string(encoded), "item-1") || strings.Contains(string(encoded), "external_key") {
			t.Fatal("source identity entered compile IR")
		}
	}
	for _, key := range []string{"", "with space", strings.Repeat("a", 129)} {
		candidate := ParseURI("http://source.example.invalid:8080?external_key=" + url.QueryEscape(key))
		if candidate.Valid() {
			t.Fatal("invalid explicit source key was silently discarded")
		}
	}
	values := syntheticValues(t).vmess()
	values["external_key"] = "vmess-stable-1"
	candidate := ParseURI(share("vmess", encodeVMess(t, values)))
	if !candidate.Valid() || origin.ExternalKeyFromMetadata(candidate.Metadata) != "vmess-stable-1" {
		t.Fatal("VMess explicit source identity was not retained")
	}
	values["external_key"] = 123
	if ParseURI(share("vmess", encodeVMess(t, values))).Valid() {
		t.Fatal("non-string source identity accepted")
	}
}
