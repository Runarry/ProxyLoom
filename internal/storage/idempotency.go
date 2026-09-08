package storage

import (
	"context"
	"crypto/hmac"
	"encoding/json"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

// ExecuteIdempotent claims, performs business writes, and persists its typed
// receipt in one transaction. A concurrent loser sees only the committed winner.
// The caller authenticates and authorizes every invocation, including replays.
// Expired receipts are retained until a separate authorized retention process;
// expiration never silently turns a previously used key into a new write.
func (c *Catalog) ExecuteIdempotent(ctx context.Context, request catalog.IdempotencyRequest,
	fn func(catalog.Tx) (catalog.Receipt, error)) (catalog.IdempotencyResult, error) {
	if fn == nil || !validIDs(request.ScopeID, request.PrincipalID) || !safeIdempotencyToken(request.RouteKey) ||
		!safeIdempotencyToken(request.Key) || len(request.CanonicalRequest) == 0 || len(request.CanonicalRequest) > 1<<20 {
		return catalog.IdempotencyResult{}, catalog.ErrInvalidInput
	}
	// JSON framing binds the scope and operation without delimiter ambiguity.
	// CanonicalRequest is produced by the strict API boundary; it is never stored.
	framed, err := json.Marshal(struct {
		Scope     ir.ID  `json:"scope"`
		Principal ir.ID  `json:"principal"`
		Route     string `json:"route"`
		Request   []byte `json:"request"`
	}{request.ScopeID, request.PrincipalID, request.RouteKey, request.CanonicalRequest})
	if err != nil {
		return catalog.IdempotencyResult{}, catalog.ErrInvalidInput
	}
	defer clear(framed)
	digest, err := c.box.Digest(secretbox.PurposeIdempotency, framed)
	if err != nil {
		return catalog.IdempotencyResult{}, catalog.ErrCrypto
	}
	var result catalog.IdempotencyResult
	err = c.transact(ctx, request.ScopeID, func(t *catalogTx) error {
		claim := dbgen.ClaimIdempotencyKeyParams{PrincipalID: dbID(request.PrincipalID), RouteKey: request.RouteKey,
			Key: request.Key, ScopeID: dbID(request.ScopeID), RequestHmac: digest}
		count, err := t.q.ClaimIdempotencyKey(ctx, claim)
		if err != nil {
			return err
		}
		if count == 0 {
			row, err := t.q.GetIdempotencyReceipt(ctx, dbgen.GetIdempotencyReceiptParams{
				PrincipalID: claim.PrincipalID, RouteKey: claim.RouteKey, Key: claim.Key})
			if err != nil {
				return err
			}
			if irID(row.ScopeID) != request.ScopeID || !hmac.Equal(row.RequestHmac, digest) {
				return catalog.ErrIdempotencyConflict
			}
			receipt := catalog.Receipt{HTTPStatus: int(row.HttpStatus.Int32), ResourceID: irID(row.ResourceID),
				Revision: row.ResourceRevision.Int64, OperationID: irID(row.OperationID), Status: catalog.ReceiptStatus(row.Status.String)}
			if receipt.Validate() != nil {
				return catalog.ErrUnavailable
			}
			result = catalog.IdempotencyResult{Receipt: receipt, Replayed: true}
			return nil
		}
		receipt, err := fn(t)
		// The business callback is over before receipt SQL starts. Its scoped
		// handle cannot race finalization or escape into another write later.
		t.close()
		if err != nil {
			return err
		}
		t.mu.Lock()
		failed := t.failed
		t.mu.Unlock()
		if failed != nil {
			return failed
		}
		if err := receipt.Validate(); err != nil {
			return err
		}
		if err := t.q.FinalizeIdempotencyReceipt(ctx, dbgen.FinalizeIdempotencyReceiptParams{
			PrincipalID: claim.PrincipalID, RouteKey: claim.RouteKey, Key: claim.Key,
			HttpStatus: pgtype.Int4{Int32: int32(receipt.HTTPStatus), Valid: true}, ResourceID: dbID(receipt.ResourceID),
			ResourceRevision: pgtype.Int8{Int64: receipt.Revision, Valid: receipt.ResourceID != ""},
			OperationID:      dbID(receipt.OperationID), Status: pgtype.Text{String: string(receipt.Status), Valid: true}}); err != nil {
			return err
		}
		result.Receipt = receipt
		return nil
	})
	if err != nil {
		return catalog.IdempotencyResult{}, err
	}
	return result, nil
}

func safeIdempotencyToken(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := range value {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
