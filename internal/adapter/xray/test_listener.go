package xray

import (
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/adapter"
)

// BindTestListener changes only the compiler's minimal probe listener port.
func BindTestListener(config []byte, port int) ([]byte, error) {
	var doc document
	if port < 20000 || port > 20127 || json.Unmarshal(config, &doc) != nil || len(doc.Inbounds) != 1 || doc.Inbounds[0].Listen != adapter.LoopbackAddr || doc.Inbounds[0].Port != ListenPort || doc.Inbounds[0].Protocol != "socks" {
		return nil, errors.New("invalid_test_listener")
	}
	doc.Inbounds[0].Port = port
	return adapter.EncodeJSON(doc)
}
