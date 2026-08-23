package service

import "encoding/json"

// rejected wraps a ServiceError into an outcome for the run helper.
func rejected(e *ServiceError) (*outcome, error) {
	return &outcome{serr: e}, nil
}

// mustJSON serializes a response value. It panics only if the value cannot be
// marshaled, which is impossible for the plain response structs used here.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
