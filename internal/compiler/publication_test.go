package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestPublicationFinalSafetyRedactionAndDeterminism(t *testing.T) {
	c := mustCompiler(t)
	input, err := ir.NewFrozenInput(routingSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range input.Spec().Targets {
		t.Run(string(target.CoreFamily), func(t *testing.T) {
			graph, _, err := c.Prepare(input, target)
			if err != nil {
				t.Fatal(err)
			}
			artifact, _, err := c.Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.PublicationEvidence(input, target); err != nil {
				t.Fatal(err)
			}
			if err = CheckPublication(artifact.Bytes, target, *graph.Preset); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				next, _, err := c.Compile(context.Background(), input, target)
				if err != nil || !bytes.Equal(next.Bytes, artifact.Bytes) {
					t.Fatal("non-deterministic final bytes")
				}
			}
			preview, err := RedactedNative(artifact.Bytes, target.Format)
			if err != nil || strings.Contains(preview, "EXAMPLE_ONLY_A") {
				t.Fatal("preview contains synthetic credential")
			}
			for _, field := range []string{"external-ui", "script", "tun", "certificate_path"} {
				doc, _ := nativeDocument(artifact.Bytes, target.Format)
				doc[field] = "forbidden"
				var bad []byte
				if target.Format == ir.MihomoYAML {
					bad, _ = yaml.Marshal(doc)
				} else {
					bad, _ = json.Marshal(doc)
				}
				if CheckPublication(bad, target, *graph.Preset) == nil {
					t.Fatalf("final %s field bypassed safety checks", field)
				}
			}
			bad := bytes.ReplaceAll(artifact.Bytes, []byte("127.0.0.1"), []byte("0.0.0.0"))
			if CheckPublication(bad, target, *graph.Preset) == nil {
				t.Fatal("non-loopback listener accepted")
			}
			doc, _ := nativeDocument(artifact.Bytes, target.Format)
			field, nameKey := "outbounds", "tag"
			if target.CoreFamily == ir.Mihomo {
				field, nameKey = "proxies", "name"
			}
			doc[field].([]any)[0].(map[string]any)[nameKey] = "GLOBAL"
			if target.Format == ir.MihomoYAML {
				bad, _ = yaml.Marshal(doc)
			} else {
				bad, _ = json.Marshal(doc)
			}
			if CheckPublication(bad, target, *graph.Preset) == nil {
				t.Fatal("reserved native proxy name was repurposed")
			}
		})
	}
}
func TestPublicationEvidenceRejectsUnreviewedAdapter(t *testing.T) {
	c := mustCompiler(t)
	input, err := ir.NewFrozenInput(routingSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range input.Spec().Targets {
		spec := input.Spec()
		for i := range spec.Targets {
			if spec.Targets[i].Key == target.Key {
				spec.Targets[i].AdapterVersion = "unreviewed"
				target = spec.Targets[i]
			}
		}
		changed, err := ir.NewFrozenInput(spec)
		if err != nil {
			t.Fatal(err)
		}
		if c.PublicationEvidence(changed, target) == nil {
			t.Fatal("unreviewed adapter accepted")
		}
	}
}
