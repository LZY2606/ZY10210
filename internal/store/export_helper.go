package store

import (
	"encoding/json"

	"dynid/internal/ident"
)

func jsonRaw(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// MarshalReport is the NaN-safe JSON encoding used by run persistence.
func MarshalReport(rep *ident.ModelReport) ([]byte, error) {
	return ident.MarshalJSONSafe(rep)
}
