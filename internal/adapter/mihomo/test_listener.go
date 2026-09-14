package mihomo

import (
	"errors"
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"gopkg.in/yaml.v3"
)

type testListener struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Listen string `yaml:"listen"`
	Port   int    `yaml:"port"`
	UDP    bool   `yaml:"udp"`
}

func BindTestListener(config []byte, port int) ([]byte, error) {
	var doc document
	if port < 20000 || port > 20127 || yaml.Unmarshal(config, &doc) != nil || doc.BindAddress != adapter.LoopbackAddr || doc.MixedPort != MixedPort || doc.SOCKSPort != 0 || doc.HTTPPort != 0 || len(doc.TestListeners) != 0 || doc.AllowLAN || doc.ExternalController != "" {
		return nil, errors.New("invalid_test_listener")
	}
	doc.MixedPort = 0
	// The legacy socks-port always creates UDP and closes TCP if UDP fails.
	// A named listener supports an explicit TCP-only entrance.
	doc.TestListeners = []testListener{{Name: "proxyloom-test", Type: "socks", Listen: adapter.LoopbackAddr, Port: port, UDP: false}}
	return marshalYAML(doc)
}
