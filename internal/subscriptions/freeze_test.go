package subscriptions_test

import (
	"encoding/json"
	"fmt"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTagSelectionAndCloneIsolation(t *testing.T) {
	tags := []string{"home", "fast"}
	for _, test := range []struct {
		selector ir.TagSelector
		want     bool
	}{{ir.TagSelector{}, false}, {ir.TagSelector{AllTags: []string{"home", "fast"}}, true}, {ir.TagSelector{AllTags: []string{"home", "absent"}}, false}, {ir.TagSelector{AllTags: []string{"home"}, AnyTags: []string{"fast", "other"}, NoneTags: []string{"disabled"}}, true}, {ir.TagSelector{AllTags: []string{"home"}, AnyTags: []string{"other"}}, false}, {ir.TagSelector{NoneTags: []string{"disabled"}}, true}, {ir.TagSelector{NoneTags: []string{"home"}}, false}} {
		if test.selector.Matches(tags) != test.want {
			t.Fatal("tag selection semantics differ")
		}
	}
	data, err := os.ReadFile("../../fixtures/ir/positive/subscription-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var original ir.SubscriptionProfile
	if err = json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	copy := original.Clone()
	copy.Members.IncludeIDs[0] = "99999999-9999-4999-8999-999999999999"
	copy.Targets[0].Key = "changed"
	if original.Members.IncludeIDs[0] == copy.Members.IncludeIDs[0] || original.Targets[0].Key == copy.Targets[0].Key {
		t.Fatal("clone aliases original subscription")
	}
}
func TestPrivateValuesAreNotLoggableAndPreviewIsBounded(t *testing.T) {
	private := "synthetic-private-value"
	for _, value := range []any{subscriptions.TokenIssue{Token: private}, subscriptions.Download{Bytes: []byte(private)}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), private) {
				t.Fatal("private publication value was logged")
			}
		}
	}
	preview, truncated := subscriptions.Preview(strings.Repeat("织流", 10000))
	if !truncated || len(preview) > 32<<10 || !utf8.ValidString(preview) {
		t.Fatal("bounded preview broke UTF-8 or size limit")
	}
}
