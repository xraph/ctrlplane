package managed

import "context"

// ProviderStepAcceptance preserves a provision permission and its independent local
// command receipt. It is historical evidence and never grants a transport send.
// Receipt and permission digests and timestamps belong to different contracts.
type ProviderStepAcceptance struct {
	Permission ProviderStepPermission `json:"permission"`
	Receipt    Receipt                `json:"receipt"`
}

// ProviderStepAcceptanceStore authenticates create-network, create-container and
// start-container acceptance using the exact original authorization request.
// Current lease/revision changes do not turn this reader into new authorization.
// Removal originals use their separately authenticated attempt associations.
type ProviderStepAcceptanceStore interface {
	ReadManagedProviderStepAcceptance(ctx context.Context, request ProviderStepAuthorization) (ProviderStepAcceptance, error)
}
