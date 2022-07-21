package errors

import (
	"testing"

	"github.com/cockroachdb/errors"
)

func TestWarningError(t *testing.T) {
	err := errors.New("foo")
	w := NewWarningError(err)
	if _, ok := w.(Warning); !ok {
		t.Error(`Expected variable "w" to be of type warning`)
	}

	if errors.Is(err, &warning{}) {
		t.Error(`Expected variable "err" to NOT be of type warning`)
	}

	if !errors.Is(w, &WarningReference) {
		t.Error(`Expected variable "err" to be of type warning`)
	}
}
