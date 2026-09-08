package chainverify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

const (
	expectCanceled = "canceled"
	expectTimedOut = "timed_out"
)

type liveCore struct {
	*coreSession
	evidence *sessionResult
	stop     func() runResult
}

func (h *harness) configEvidence(config []byte, termination string) *sessionResult {
	entry := &sessionResult{
		TestConfigSHA256:    fmt.Sprintf("%x", sha256.Sum256(config)),
		ExpectedTermination: termination, CleanupStatus: "pending",
	}
	h.scenario.Sessions = append(h.scenario.Sessions, entry)
	return entry
}

func (h *harness) startCore(config []byte) *liveCore {
	t := h.t
	t.Helper()
	entry := h.configEvidence(config, expectCanceled)
	session, err := startCore(h.registry, h.fam, config)
	if err != nil {
		entry.CleanupStatus = "startup_failed"
		entry.CleanupError = reportError(err)
		t.Fatal(err)
	}
	return manageCore(t, session, entry)
}

func manageCore(t *testing.T, session *coreSession, entry *sessionResult) *liveCore {
	t.Helper()
	return &liveCore{
		coreSession: session, evidence: entry,
		stop: observeCleanup(t, entry, session.stop),
	}
}

func observeCleanup(t *testing.T, entry *sessionResult, finish func() runResult) func() runResult {
	t.Helper()
	var once sync.Once
	var res runResult
	stop := func() runResult {
		t.Helper()
		once.Do(func() {
			res = finish()
			entry.ExitCode, entry.Canceled, entry.TimedOut = res.result.ExitCode, res.result.Canceled, res.result.TimedOut
			entry.CleanupStatus = "pass"
			if err := terminationError(res, entry.ExpectedTermination); err != nil {
				entry.CleanupStatus = "fail"
				entry.CleanupError = reportError(err)
				t.Errorf("core execution or cleanup failed: %v", err)
			}
		})
		return res
	}
	t.Cleanup(func() { stop() })
	return stop
}

func terminationError(res runResult, expected string) error {
	err := res.err
	// Cancellation and timeout permit the resulting process exit code, but
	// never exempt an execution, workspace, or inbound cleanup error.
	switch expected {
	case expectCanceled:
		if !res.result.Canceled || res.result.TimedOut {
			err = errors.Join(err, fmt.Errorf("expected canceled core, got canceled=%t timed_out=%t exit=%d", res.result.Canceled, res.result.TimedOut, res.result.ExitCode))
		}
	case expectTimedOut:
		if !res.result.TimedOut || res.result.Canceled {
			err = errors.Join(err, fmt.Errorf("expected timed out core, got canceled=%t timed_out=%t exit=%d", res.result.Canceled, res.result.TimedOut, res.result.ExitCode))
		}
	default:
		err = errors.Join(err, fmt.Errorf("unknown expected termination %q", expected))
	}
	return err
}

func (h *harness) probe(session *liveCore, requestID, host string, port int) ([]byte, error) {
	body, err := session.probe(requestID, host, port)
	ok, remote, returnedID := parseProbe(body)
	h.scenario.Probes = append(h.scenario.Probes, probeResult{
		TestConfigSHA256: session.evidence.TestConfigSHA256,
		RequestID:        requestID, ReturnedRequestID: returnedID,
		ExitIP: isolation.RemoteIP(remote), OK: ok, Error: reportError(err),
	})
	if ok && returnedID != requestID {
		h.t.Errorf("probe request ID %q, want %q", returnedID, requestID)
	}
	return body, err
}

func (h *harness) observeTraffic(topo *topo, phase string) func() {
	entry := h.scenario
	before := trafficSnapshot(topo)
	return func() {
		after := trafficSnapshot(topo)
		entry.Traffic = append(entry.Traffic, trafficResult{
			Phase:                 phase,
			TargetAcceptDelta:     after.TargetAcceptDelta - before.TargetAcceptDelta,
			TargetOKDelta:         after.TargetOKDelta - before.TargetOKDelta,
			AOKDelta:              after.AOKDelta - before.AOKDelta,
			BOKDelta:              after.BOKDelta - before.BOKDelta,
			AForbiddenSourceDelta: after.AForbiddenSourceDelta - before.AForbiddenSourceDelta,
			BForbiddenSourceDelta: after.BForbiddenSourceDelta - before.BForbiddenSourceDelta,
		})
	}
}

func trafficSnapshot(topo *topo) trafficResult {
	return trafficResult{
		TargetAcceptDelta: topo.TargetLog.Count("accept"),
		TargetOKDelta:     topo.TargetLog.Count("ok"),
		AOKDelta:          topo.A.Log.Count("ok"), BOKDelta: topo.B.Log.Count("ok"),
		AForbiddenSourceDelta: topo.A.Log.Count("forbidden_source"),
		BForbiddenSourceDelta: topo.B.Log.Count("forbidden_source"),
	}
}
