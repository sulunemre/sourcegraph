package errors

// Warning embeds an error. It's purpose is to indicate that this error is not a critical error and
// maybe ignored. Additionally, it **must** be logged only as a warning. If it cannot be logged as a
// warning, then these are not the droids you're looking for.
type Warning interface {
	error
	IsWarning() bool
}

type warning struct {
	Err error
}

// Ensure that warning always implements the Warning error interface.
var _ Warning = (*warning)(nil)

var WarningReference = warning{}

// NewWarningError will return an error of type warning. This should be used to wrap errors where we
// do not intend to return an error, increment an error metric. That is, if an error is returned and
// it is not critical and / or expected to be intermittent and / or nothing we can do about
// (example: 404 errors from upstream code host APIs in repo syncing), we should wrap the error with
// NewWarningError.
//
// Consumers of these errors should then use errors.Is to check if the error is of a warning type
// and based on that, should just log it as a warning. For example:
//
// TODO
// err := someFunctionThatReturnsAWarningErrorOrACriticalError()
// TODO
//
func NewWarningError(err error) error {
	return &warning{
		Err: err,
	}
}

func (ce *warning) Error() string {
	return ce.Err.Error()
}

// IsWarning always returns true. It exists to differentiate regular errors with Warning
// errors. That is, all Warning type objects are error types, but not all error types are Warning
// types.
func (w *warning) IsWarning() bool {
	return true
}
