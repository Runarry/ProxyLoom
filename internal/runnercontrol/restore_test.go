package runnercontrol_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

func TestRestoreEpochRejectsExistingMTLSRegistrationAndDatabaseFailure(t *testing.T) {
	pki := makePKI(t)
	builds := pinnedBuilds(t)
	var epoch atomic.Value
	epoch.Store("")
	lookup := func(context.Context) (string, error) {
		v := epoch.Load().(string)
		if v == "unavailable" {
			return "", errors.New("synthetic unavailable")
		}
		return v, nil
	}
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{registration(pki, builds)}, Jobs: &fakeQueue{}, AuthorizationEpoch: lookup})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(control)
	server.TLS = pki.server
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: pki.client}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	request := runnerprotocol.RegisterRequest{RunnerID: runnerID, Platform: "linux", Architecture: "amd64", AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}, Builds: []runnerprotocol.BuildReport{{CoreBuildID: builds[0].ID, BuildSHA256: builds[0].BinarySHA256}}}
	endpoint := server.URL + "/internal/v1/runners/heartbeat"
	if status := post(t, client, endpoint, request, nil); status != 200 {
		t.Fatal("initial registration rejected", status)
	}
	epoch.Store(string(jobs.NewID()))
	if status := post(t, client, endpoint, request, nil); status != 403 {
		t.Fatal("old registration survived restored epoch", status)
	}
	epoch.Store("unavailable")
	if status := post(t, client, endpoint, request, nil); status != 503 {
		t.Fatal("registration accepted without epoch authority", status)
	}
}
