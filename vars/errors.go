package vars

import "errors"

var (
	// ErrInvalidDefinition indicates a malformed variable definition.
	ErrInvalidDefinition = errors.New("vars: invalid variable definition")

	// ErrMissingRequired indicates a required variable had no value or default.
	ErrMissingRequired = errors.New("vars: required variable not provided")

	// ErrInvalidValue indicates a value failed type, enum, or pattern validation.
	ErrInvalidValue = errors.New("vars: invalid variable value")

	// ErrCycle indicates computed variables form an unresolvable cycle or
	// reference a variable that is never defined.
	ErrCycle = errors.New("vars: computed variable cycle or undefined reference")
)
