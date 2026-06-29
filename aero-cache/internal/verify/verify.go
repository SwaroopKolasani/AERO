package verify

import (
	"bytes"

	"aero-cache/internal/key"
	"aero-cache/internal/store"

	"lukechampine.com/blake3"
)

type Result struct {
	OK     bool
	Reason string
}

func Entry(material *key.Material, entry *store.Entry) Result {
	if material == nil {
		return fail("nil_material")
	}
	if entry == nil {
		return fail("nil_entry")
	}

	if entry.Epoch != material.Epoch {
		return fail("epoch_mismatch")
	}

	if entry.Fingerprint != material.Fingerprint {
		return fail("fingerprint_mismatch")
	}

	if !equalUint32s(entry.TokenIDs, material.TokenIDs) {
		return fail("token_ids_mismatch")
	}

	if !bytes.Equal(entry.Params, material.CanonicalParams) {
		return fail("params_mismatch")
	}

	sum := blake3.Sum256(entry.Response)
	if sum != entry.RespHash {
		return fail("response_hash_mismatch")
	}

	return Result{OK: true, Reason: "verified"}
}

func fail(reason string) Result {
	return Result{OK: false, Reason: reason}
}

func equalUint32s(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}

	var diff uint32
	for i := range a {
		diff |= a[i] ^ b[i]
	}

	return diff == 0
}
