package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
)

func TestAuthenticationConfigurationAndCookieTransportPolicy(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		publicURL   string
		development bool
		trusted     []string
		accepted    bool
		secure      bool
	}{
		{"https", testAuthOrigin, false, nil, true, true},
		{"https_in_development", testAuthOrigin, true, nil, true, true},
		{"https_with_base_path", testAuthOrigin + "/proxyloom/", false, nil, true, true},
		{"loopback_development", "http://127.0.0.1:8080", true, nil, true, false},
		{"ipv6_loopback_development", "http://[::1]:8080", true, nil, true, false},
		{"localhost_development", "http://localhost:8080", true, nil, true, false},
		{"loopback_production", "http://127.0.0.1:8080", false, nil, false, false},
		{"remote_development", "http://admin.proxyloom.invalid", true, nil, false, false},
		{"empty_origin", "", false, nil, false, false},
		{"public_userinfo", "https://EXAMPLE_SECRET@admin.proxyloom.invalid", false, nil, false, false},
		{"public_fragment", testAuthOrigin + "#", false, nil, false, false},
		{"public_query", testAuthOrigin + "?", false, nil, false, false},
		{"invalid_scheme", "file:///tmp/index", false, nil, false, false},
		{"invalid_port", testAuthOrigin + ":70000", false, nil, false, false},
		{"empty_port", testAuthOrigin + ":", false, nil, false, false},
		{"invalid_hostname", "https://admin.proxyloom.invalid.EXAMPLE_SECRET_", false, nil, false, false},
		{"scoped_ipv6", "https://[fe80::1%25eth0]", false, nil, false, false},
		{"trusted_cidrs", testAuthOrigin, false, []string{"10.0.0.0/8", "2001:db8::/32"}, true, true},
		{"bare_proxy_ip", testAuthOrigin, false, []string{"10.0.0.1"}, false, false},
		{"invalid_proxy", testAuthOrigin, false, []string{"EXAMPLE_UNTRUSTED_PROXY"}, false, false},
		{"mapped_proxy_cidr", testAuthOrigin, false, []string{"::ffff:10.0.0.0/104"}, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newFakeIdentity()
			authentication, err := NewAuthentication(service, AuthenticationConfig{PublicURL: scenario.publicURL, Development: scenario.development, TrustedProxies: scenario.trusted})
			if !scenario.accepted {
				if err == nil || strings.Contains(err.Error(), "EXAMPLE_") {
					t.Fatal("unsafe transport configuration accepted or leaked")
				}
				return
			}
			if err != nil || authentication.secureCookie != scenario.secure {
				t.Fatalf("unexpected transport policy: %v", err)
			}
			dependencies := authDependencies(service, scenario.publicURL)
			dependencies.Development = scenario.development
			dependencies.TrustedProxies = scenario.trusted
			handler, _ := testHandler(t, dependencies, io.Discard)
			r := authRequest(http.MethodPost, "/api/v1/auth/login", loginJSON())
			origin := scenario.publicURL
			origin = strings.TrimSuffix(origin, "/proxyloom/")
			r.Header.Set("Origin", origin)
			r.Header.Set("X-Forwarded-Proto", "http")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			cookies := response.Result().Cookies()
			if response.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Secure != scenario.secure {
				t.Fatalf("configured cookie policy not applied: %d", response.Code)
			}
		})
	}
}

func TestTrustedProxySourceResolution(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		peer       string
		trusted    []string
		forwarded  []string
		real       []string
		expectedIP string
	}{
		{"no_proxy_trust", "192.0.2.5:1234", nil, []string{"198.51.100.10"}, nil, "192.0.2.5"},
		{"untrusted_ignores_malformed", "192.0.2.5:1234", []string{"10.0.0.0/8"}, []string{"EXAMPLE_FORGED_IP", ""}, []string{"EXAMPLE_FORGED_IP", ""}, "192.0.2.5"},
		{"trusted_one_hop", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"198.51.100.10"}, nil, "198.51.100.10"},
		{"trusted_right_to_left", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"203.0.113.99, 198.51.100.10, 10.0.0.6"}, nil, "198.51.100.10"},
		{"all_trusted_chain", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"10.0.0.9, 10.0.0.6"}, nil, "10.0.0.9"},
		{"trusted_real_fallback", "10.0.0.5:1234", []string{"10.0.0.0/8"}, nil, []string{"198.51.100.11"}, "198.51.100.11"},
		{"trusted_no_header", "10.0.0.5:1234", []string{"10.0.0.0/8"}, nil, nil, "10.0.0.5"},
		{"mapped_peer", "[::ffff:192.0.2.5]:1234", nil, nil, nil, "192.0.2.5"},
		{"mapped_forwarded", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"::ffff:198.51.100.10"}, nil, "198.51.100.10"},
		{"ipv6_chain", "[2001:db8:1::1]:1234", []string{"2001:db8:1::/48"}, []string{"2001:db8:2::2, 2001:db8:1::2"}, nil, "2001:db8:2::2"},
		{"duplicate_forwarded", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"198.51.100.10", "198.51.100.10"}, nil, ""},
		{"empty_forwarded", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{""}, nil, ""},
		{"malformed_left_of_untrusted", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"EXAMPLE_FORGED_IP, 198.51.100.10"}, nil, ""},
		{"forwarded_port", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"198.51.100.10:1234"}, nil, ""},
		{"forwarded_zone", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{"fe80::1%eth0"}, nil, ""},
		{"too_many_forwarded", "10.0.0.5:1234", []string{"10.0.0.0/8"}, []string{strings.Repeat("198.51.100.10,", 32) + "198.51.100.10"}, nil, ""},
		{"duplicate_real", "10.0.0.5:1234", []string{"10.0.0.0/8"}, nil, []string{"198.51.100.10", "198.51.100.10"}, ""},
		{"list_real", "10.0.0.5:1234", []string{"10.0.0.0/8"}, nil, []string{"198.51.100.10, 198.51.100.11"}, ""},
		{"malformed_peer", "EXAMPLE_PRIVATE_PEER", nil, nil, nil, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newFakeIdentity()
			dependencies := authDependencies(service, testAuthOrigin)
			dependencies.TrustedProxies = scenario.trusted
			var logs bytes.Buffer
			handler, _ := testHandler(t, dependencies, &logs)
			r := authRequest(http.MethodPost, "/api/v1/auth/login", loginJSON())
			r.RemoteAddr = scenario.peer
			r.Header["X-Forwarded-For"] = scenario.forwarded
			r.Header["X-Real-Ip"] = scenario.real
			r.Host = "EXAMPLE_UNTRUSTED_HOST.invalid"
			r.Header.Set("X-Forwarded-Host", "EXAMPLE_UNTRUSTED_HOST.invalid")
			r.Header.Set("X-Forwarded-Proto", "http")
			r.Header.Set("X-Request-ID", "EXAMPLE_UNTRUSTED_REQUEST_ID")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if scenario.expectedIP == "" {
				requireErrorResponse(t, response, http.StatusBadRequest, apicontract.MalformedRequest)
				if service.calls["login"] != 0 {
					t.Fatal("ambiguous trusted source reached rate-limit service")
				}
			} else if response.Code != http.StatusOK || service.lastSource.IP != scenario.expectedIP || service.lastSource.RequestID != response.Header().Get("X-Request-ID") || !identity.ValidSource(service.lastSource) {
				t.Fatalf("unexpected trusted source or status: %d %#v", response.Code, service.lastSource)
			}
			if strings.Contains(logs.String(), "EXAMPLE_") {
				t.Fatal("raw forwarding, host or request ID value leaked to access log")
			}
		})
	}
}

func TestAuthRegisteredRoutesHaveSafeLogNames(t *testing.T) {
	var logs bytes.Buffer
	service := newFakeIdentity()
	handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), &logs)
	for path, methods := range handler.Routes() {
		r := authRequest(strings.ToUpper(methods[0]), path+"?token=EXAMPLE_QUERY_SECRET", "EXAMPLE_BODY_SECRET")
		r.Header.Set("Authorization", "Bearer EXAMPLE_AUTH_SECRET")
		r.Header.Set("Cookie", SessionCookieName+"=EXAMPLE_COOKIE_SECRET")
		r.Header.Set("X-Request-ID", "EXAMPLE_REQUEST_SECRET")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if !strings.Contains(logs.String(), `"route":"`+path+`"`) || strings.Contains(response.Body.String(), "EXAMPLE_") {
			t.Fatal("auth route diagnostic missing or response exposed a secret")
		}
	}
	if strings.Contains(logs.String(), "EXAMPLE_") {
		t.Fatal("auth request secret leaked into logs")
	}
}
