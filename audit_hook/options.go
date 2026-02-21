package audithook

import "log/slog"

// Option configures the audit hook extension.
type Option func(*Extension)

// WithActions limits which audit actions are recorded.
// By default all actions are recorded. If WithActions is used,
// only the listed actions will be emitted.
func WithActions(actions ...string) Option {
	return func(e *Extension) {
		e.enabled = make(map[string]bool, len(actions))
		for _, a := range actions {
			e.enabled[a] = true
		}
	}
}

// WithLogger sets a custom logger for the audit hook.
func WithLogger(l *slog.Logger) Option {
	return func(e *Extension) {
		e.logger = l
	}
}
