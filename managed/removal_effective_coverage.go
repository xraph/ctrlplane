package managed

// EffectiveStepDisposition distinguishes never-issued slots from resolved invocations.
// No unresolved or uncertain observation is a terminal coverage disposition.
type EffectiveStepDisposition string

const (
	// EffectiveStepUnissued requires the original fixed selection to be unissued.
	EffectiveStepUnissued EffectiveStepDisposition = "unissued"
	// EffectiveStepRemoved requires the selected invocation's exact positive result.
	EffectiveStepRemoved EffectiveStepDisposition = "removed"
	// EffectiveStepClosed requires affirmative before-delegation custody closure.
	EffectiveStepClosed EffectiveStepDisposition = "closed_no_dispatch"
)

// EffectiveRemovalBinding pins one original binding and an independently accepted
// native guard proof. The enclosing coverage commits the ordered leaf associations.
// Historical guard proof expiry does not fabricate current process liveness.
type EffectiveRemovalBinding struct {
	Binding RemovalRecoveryBinding `json:"binding"`
	Proof   RecoveryAssociation    `json:"proof"`
}
