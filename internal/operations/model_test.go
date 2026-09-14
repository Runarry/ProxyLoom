package operations

import "testing"

func TestPrivateProxyCIDRsAreNarrowAndCanonical(t *testing.T) {
	settings := Defaults()
	settings.PrivateProxyCIDRs = []string{"192.168.5.0/24", "10.10.0.0/16"}
	prefixes, err := settings.PrivateProxyPrefixes()
	if err != nil || len(prefixes) != 2 || prefixes[0].String() != "10.10.0.0/16" {
		t.Fatalf("valid proxy CIDRs rejected or not canonicalized: %v %#v", err, prefixes)
	}
	for _, value := range [][]string{{"192.168.5.1/24"}, {"192.0.2.0/24"}, {"192.168.5.0/24", "192.168.5.0/24"}, {"192.168.0.0/15"}} {
		settings.PrivateProxyCIDRs = value
		if settings.Validate() == nil {
			t.Fatalf("invalid proxy CIDR settings accepted: %#v", value)
		}
	}
}
