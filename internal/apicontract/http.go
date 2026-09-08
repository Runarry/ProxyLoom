package apicontract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
)

type requestIDKey struct{}

// RequestIDs supplies a fresh server-owned identifier. Client X-Request-ID is
// deliberately ignored, so headers cannot inject credentials into error/log IDs.
func RequestIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			WriteError(w, "unavailable", NewError(InternalError))
			return
		}
		id := hex.EncodeToString(bytes[:])
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return safeRequestID(id)
}

var etagPattern = regexp.MustCompile(`^"r([1-9][0-9]{0,18})"$`)

func ETag(revision int64) (string, error) {
	if revision < 1 {
		return "", NewError(InternalError)
	}
	return `"r` + strconv.FormatInt(revision, 10) + `"`, nil
}

// ParseIfMatch requires one strong resource tag. Wildcards, lists, weak tags,
// leading zeroes, overflow and duplicate header lines are malformed (400).
func ParseIfMatch(header http.Header) (int64, error) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		return 0, NewError(PreconditionRequired)
	}
	if len(values) != 1 {
		return 0, NewError(MalformedRequest)
	}
	match := etagPattern.FindStringSubmatch(values[0])
	if match == nil {
		return 0, NewError(MalformedRequest)
	}
	revision, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, NewError(MalformedRequest)
	}
	return revision, nil
}

// RequireRevision checks HTTP syntax and an observed revision; the repository
// must still compare expected_revision inside the mutation transaction.
func RequireRevision(header http.Header, current int64) (int64, error) {
	expected, err := ParseIfMatch(header)
	if err != nil {
		return 0, err
	}
	if current < 1 {
		return 0, NewError(InternalError)
	}
	if current != expected {
		return 0, NewError(RevisionMismatch)
	}
	return expected, nil
}
