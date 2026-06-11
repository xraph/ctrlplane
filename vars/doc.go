// Package vars defines first-class, typed template variables and the
// resolver that turns variable definitions plus caller-supplied values
// into a resolution scope used to render deployment sources.
//
// Variables may be plain (string/int/bool/enum) with defaults and
// validation, secret-typed (resolved to a
// [github.com/xraph/ctrlplane/provider.SecretBinding] and never inlined
// into rendered output), or computed from a Go text/template expression
// over previously-resolved variables and derived context.
package vars
